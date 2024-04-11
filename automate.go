package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	api_gh "github.com/google/go-github/github"
	ver_compare "github.com/hashicorp/go-version"
)

type Project struct { // alphabetical order
	Cleanup     []string `mapstructure:"cleanup"`
	Commands    []string `mapstructure:"commands"`
	DependsOn   []string `mapstructure:"depends_on"`
	Environment string   `mapstructure:"environment"`
	Triggers    []string `mapstructure:"triggers"`
}

type Commands struct { // alphabetical order
	Cleanup     []string `mapstructure:"cleanup"`
	Commands    []string `mapstructure:"commands"`
	Environment string   `mapstructure:"environment"`
}

func evaluateRelease(value string) (oldValue, newValue string, runIt bool, err error) {
	owner, value, _ := strings.Cut(value, "/")
	repo, oldValue, _ := strings.Cut(value, "/")
	var releases []*api_gh.RepositoryRelease
	var options api_gh.ListOptions
	options.Page, options.PerPage = 1, 1
	if releases, _, err = client.Repositories.ListReleases(context.Background(), owner, repo, &options); err != nil {
		err = fmt.Errorf("error getting latest release for %s/%s: %s", owner, repo, err.Error())
	} else {
		newValue = *releases[0].TagName
		var old, new *ver_compare.Version
		if old, err = ver_compare.NewVersion(oldValue); err == nil {
			if new, err = ver_compare.NewVersion(newValue); err == nil {
				if new.GreaterThan(old) {
					logger.Debug(spf("New version %s is greater than old version %s.", newValue, oldValue))
					runIt = true
				}
			}
		}
	}
	return oldValue, newValue, runIt, err
}

func runCommand(command string) (err error) {
	logger.Debug(spf("Running command: %s", command))
	command = strings.TrimSpace(command)
	c := strings.Split(command, " ")
	cmd := exec.Command(c[0], c[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		err = fmt.Errorf("error running command %s: %s", command, err.Error())
		return err
	} else {
		logger.Debug(spf("Successfully ran command: %s", command))
	}
	return err
}

func execBlock(project string) (err error) {
	commands := conf.GetStringSlice("projects." + project + ".commands")
	defer cleanup()
	running = true
	// run the commands
	for _, command := range commands {
		if err = runCommand(command); err != nil {
			break
		}
	}
	running = false
	return err
}

func runProject() (err error) {
	c := *new(Commands)
	if err = conf.UnmarshalKey("projects."+project, &c); err != nil {
		logger.Error(spf("Error understanding %s for project %s: %s.", project, err))
	} else {
		// write the environment file
		if conf.IsSet("projects." + project + ".environment") {
			if err = os.WriteFile("environment.txt", []byte(c.Environment), 0644); err != nil {
				logger.Error(spf("Error writing environment file: %s", err.Error()))
			} else {
				logger.Debug(spf("Wrote environment file for %s.", project))
			}
		}
		// run the commands
		err = execBlock(project)
	}
	return err
}

func checkProject() {
	// check the project for the factor
	logger.Info(spf("Checking project %s... ", project))
	// get the project configuration
	p := *new(Project)
	if err := conf.UnmarshalKey("projects."+project, &p); err != nil {
		logger.Error(spf("Error understanding project %s in %s: %s.", project, conf.ConfigFileUsed(), err))
		logger.Error(spf("Not running any trigger(s) for %s.", project))
	} else { // run the triggers
		for _, t := range p.Triggers {
			trigger, value, _ := strings.Cut(t, "=")
			// get the latest value
			switch trigger {
			case "on_demand": // never run for on_demand
				logger.Debug(spf("Ignoring on_demand trigger for %s...", project))
				continue
			case "release":
				logger.Debug(spf("Getting latest tag_name for %s...", project))
				if oldValue, newValue, runIt, err := evaluateRelease(value); err != nil {
					if strings.Contains(err.Error(), "429 You have triggered an abuse detection mechanism.") {
						logger.Error(spf("You have triggered an abuse detection mechanism on GH API. Sleeping for %s...", defaultSleepMinutes.String()))
						time.Sleep(defaultSleepMinutes) // sleep for defaultSleepMinutes minutes
					} else {
						logger.Error(spf("Error getting latest %s for %s: %s", trigger, project, err.Error()))
					}
					continue
				} else {
					if !runIt {
						logger.Debug(spf("Not running %s for %s because it's not time yet.", trigger, project))
						continue
					} else {
						logger.Info(spf("Running %s for trigger called %s.", project, trigger))
						mainProject := project
						// run the dependencies
						for _, d := range p.DependsOn {
							project = d // change the project to the dependency
							if err = runProject(); err != nil {
								logger.Error(spf("Error running dependency %s for trigger %s, project %s: %s", d, trigger, project, err.Error()))
								break
							} else {
								logger.Info(spf("Ran dependecy %s for project %s successfully.", d, project))
							}
						}
						// run the project
						if err == nil {
							project = mainProject // change the project back to the main project
							// run the project
							if err = runProject(); err != nil {
								logger.Error(spf("Error running %s for project %s: %s", trigger, project, err.Error()))
							} else {
								// replace the existing entry from triggers
								for i, t := range p.Triggers {
									if t == spf("%s=%s", trigger, oldValue) { // only when the trigger is the same and has an `=` sign
										p.Triggers[i] = spf("%s=%s", trigger, newValue)
									}
								}
								// write the new configuration
								conf.Set("projects."+project, p)
								logger.Debug(spf("Updated triggers for %s.", project))
								logger.Info(spf("Ran trigger called %s for %s successfully.", trigger, project))
							}
						}
					}
				}
			default:
				logger.Error(spf("ignored unknown trigger type %s", trigger))
			}
		}
	}
}

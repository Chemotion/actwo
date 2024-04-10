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
	Environment string   `mapstructure:"environment"`
	Location    *string  `mapstructure:"location"`
	Repository  *string  `mapstructure:"repository"`
	Triggers    []string `mapstructure:"triggers"`
}

func getLatest(what string, p *Project) (answer string, err error) {
	owner_repo := strings.Split(*p.Repository, "/")
	var releases []*api_gh.RepositoryRelease
	var options api_gh.ListOptions
	options.Page, options.PerPage = 1, 1
	if releases, _, err = client.Repositories.ListReleases(context.Background(), owner_repo[0], owner_repo[1], &options); err != nil {
		err = fmt.Errorf("error getting latest %s: %s", what, err.Error())
	} else {
		switch what {
		case "tag_name":
			answer = *releases[0].TagName
		default:
			answer = ""
			err = fmt.Errorf("panic ! Don't know how to getLatest for %s", what)
		}
	}
	return answer, err
}

func shouldRun(trigger, oldValue, newValue string) (runIt bool, err error) {
	runIt = false
	switch trigger {
	case "tag_name":
		var old, new *ver_compare.Version
		if old, err = ver_compare.NewVersion(oldValue); err == nil {
			if new, err = ver_compare.NewVersion(newValue); err == nil {
				if new.GreaterThan(old) {
					logger.Debug(spf("New version %s is greater than old version %s.", newValue, oldValue))
					runIt = true
				}
			}
		}
	default:
		err = fmt.Errorf("panic ! Don't know how to evaluate shouldRun for %s", trigger)
	}
	if err != nil {
		err = fmt.Errorf("error evaluating shouldRun for %s: %s", trigger, err.Error())
	}
	return runIt, err
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
		logger.Debug(spf("Running command: %s", command))
		if err = runCommand(command); err != nil {
			break
		}
	}
	running = false
	return err
}

func checkProject() {
	// check the project for the factor
	logger.Info(spf("Checking project %s... ", project))
	// get the project configuration
	p := new(Project)
	var err error
	if err = conf.UnmarshalKey("projects."+project, &p); err != nil {
		logger.Error(spf("Error understanding project %s in %s: %s.", project, conf.ConfigFileUsed(), err))
		logger.Error(spf("Not running any trigger(s) for %s.", project))
		return
	}
	for _, t := range p.Triggers {
		t_o := strings.Split(t, "=")
		var (
			trigger  string = t_o[0]
			oldValue string = t_o[1]
			newValue string
			runIt    bool
		)
		// get the latest value
		switch trigger {
		case "tag_name":
			logger.Debug(spf("Getting latest tag_name for %s...", project))
			newValue, err = getLatest("tag_name", p)
		default:
			err = fmt.Errorf("ignored unknown trigger type %s", trigger)
		}
		if err != nil {
			if strings.Contains(err.Error(), "429 You have triggered an abuse detection mechanism.") {
				logger.Error(spf("Error getting latest %s for %s: %s. Sleeping for %s...", trigger, project, err.Error(), defaultSleepMinutes.String()))
				time.Sleep(defaultSleepMinutes) // sleep for defaultSleepMinutes minutes
			} else {
				logger.Error(spf("Error getting latest %s for %s: %s", trigger, project, err.Error()))
			}
			continue
		} else {
			// compare the values; if the comparison is true, run the project
			logger.Debug(spf("Comparing %s for %s: %s vs %s...", trigger, project, oldValue, newValue))
			if runIt, err = shouldRun(trigger, oldValue, newValue); !runIt {
				logger.Debug(spf("Not running %s for %s because it's not time yet.", project, trigger))
			} else {
				logger.Info(spf("Running %s for trigger called %s.", project, trigger))
				var pwd string
				if pwd, err = os.Getwd(); err != nil {
					err = fmt.Errorf("error getting current working directory: %s", err.Error())
				} else {
					logger.Debug(spf("Changing to %s...", *p.Location))
					if err = os.Chdir(*p.Location); err != nil {
						err = fmt.Errorf("error changing to %s: %s", *p.Location, err.Error())
					} else {
						logger.Debug(spf("Changed to %s. Now executing...", *p.Location))
						// write the environment file
						if err = os.WriteFile("environment.txt", []byte(p.Environment), 0644); err != nil {
							logger.Error(spf("Error writing environment file: %s", err.Error()))
						} else {
							if err = execBlock(project); err != nil {
								// if _, err = fmt.Println(newValue); err != nil {
								logger.Error(spf("Error running trigger called %s for project %s: %s", trigger, project, err.Error()))
							} else {
								logger.Info(spf("Ran trigger called %s for %s successfully.", trigger, project))
								// replace the existing entry from triggers
								for i, t := range p.Triggers {
									if t == spf("%s=%s", trigger, oldValue) {
										p.Triggers[i] = spf("%s=%s", trigger, newValue)
									}
								}
								// write the new configuration
								conf.Set("projects."+project, p)
								logger.Debug(spf("Updated triggers for %s.", project))
							}
						}
						logger.Debug(spf("Changing back to %s...", pwd))
						if err = os.Chdir(pwd); err != nil {
							err = fmt.Errorf("error changing back to %s: %s", pwd, err.Error())
							logger.Error(spf("Error running %s for project %s: %s", trigger, project, err.Error()))
							sigs <- os.Interrupt // exit gently if unable to get back to working directory for some reason
						}
					}
				}
			}
		}
		if err != nil {
			logger.Error(spf("Error running %s for project %s: %s", trigger, project, err.Error()))
			continue
		}
	}
}

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
	Depends_On  []string `mapstructure:"depends_on"`
	Environment []string `mapstructure:"environment"`
	Kill        []string `mapstructure:"kill"`
	Run         []string `mapstructure:"run"`
	Triggers    []string `mapstructure:"triggers"`
}

func evaluateRelease(value string) (meta map[string]string, err error) {
	meta = make(map[string]string)
	meta["runIt"] = "false"
	meta["owner"], value, _ = strings.Cut(value, "/")
	meta["repo"], meta["old"], _ = strings.Cut(value, "/")
	meta["new"] = meta["old"]
	var releases []*api_gh.RepositoryRelease
	var options api_gh.ListOptions
	options.Page, options.PerPage = 1, 1
	if releases, _, err = client.Repositories.ListReleases(context.Background(), meta["owner"], meta["repo"], &options); err != nil {
		err = fmt.Errorf("error getting latest release for %s/%s: %s", meta["owner"], meta["repo"], err.Error())
	} else {
		meta["new"] = *releases[0].TagName
		var old, new *ver_compare.Version
		if old, err = ver_compare.NewVersion(meta["old"]); err == nil {
			if new, err = ver_compare.NewVersion(meta["new"]); err == nil {
				if new.GreaterThan(old) {
					logger.Debug(spf("New version %s is greater than old version %s.", meta["new"], meta["old"]))
					meta["runIt"] = "true"
					meta["env_0"] = spf("VERSION=%s", new.String())
					meta["env_1"] = spf("TAG_NAME=%s", meta["new"])
				}
			}
		}
	}
	// meta has the following keys: runIt, owner, repo, old, new, env_0, env_1, ...
	return meta, err
}

func runCommands(commands []string, environ []string) (err error) {
	for _, command := range commands {
		logger.Debug(spf("Running command: %s", command))
		command = strings.TrimSpace(command)
		c := strings.Split(command, " ")
		runner = exec.Command(c[0], c[1:]...)
		runner.Env = environ
		runner.Stdin, runner.Stdout, runner.Stderr = nil, os.Stdout, os.Stderr
		if err = runner.Start(); err != nil {
			err = fmt.Errorf("error starting command %s: %s", command, err.Error())
			break // break immediately
		} else {
			logger.Debug(spf("Successfully started command: %s", command))
			if err = runner.Wait(); err != nil {
				err = fmt.Errorf("error running command %s: %s", command, err.Error())
				break // break immediately
			} else {
				logger.Debug(spf("Successfully ran command: %s", command))
			}
		}
	}
	return err
}

func runProject(p *Project, name string, meta *map[string]string) (err error) {
	environ := []string{}
	copy(kill, p.Kill)        // copy the kill commands
	for k, v := range *meta { // add env_ data from meta as environment
		if strings.HasPrefix(k, "env_") {
			environ = append(environ, v)
		}
	}
	environ = append(environ, p.Environment...) // add the environment as given in the configuration for the triggered project
	// run any dependencies first
	for _, dependecy := range p.Depends_On {
		if len(conf.GetStringSlice("projects."+dependecy+".triggers")) != 0 {
			logger.Debug(spf("Local triggers for dependency project %s are being ignored", dependecy)) // ignoring local triggers
		}
		if len(conf.GetStringSlice("projects."+dependecy+".depends_on")) != 0 {
			logger.Debug(spf("Local dependencies for dependency project %s are being ignored", dependecy)) // ignoring local dependencies
		}
		if len(conf.GetStringSlice("projects."+dependecy+".kill")) != 0 {
			copy(kill, conf.GetStringSlice("projects."+dependecy+".kill")) // copy the kill commands
			logger.Debug(spf("Kill commands replaced with those of the dependency project %s.", dependecy))
		}
		dependentEnv := append(environ, conf.GetStringSlice("projects."+dependecy+".environment")...) // add the environment as given in the configuration for the depemdent project
		dependentEnv = append(dependentEnv, os.Environ()...)                                          // then add the current environment so as to override the configuration
		logger.Debug(spf("Environment for the dependency %s is: %v", dependecy, dependentEnv))
		if err = runCommands(conf.GetStringSlice("projects."+dependecy+".run"), dependentEnv); err != nil {
			logger.Error(spf("Error running dependency %s for project %s: %s", dependecy, name, err.Error()))
			break
		} else {
			logger.Info(spf("Ran dependecy %s for project %s successfully.", dependecy, name))
		}
	}
	if err == nil {
		copy(kill, p.Kill) // copy the kill commands
		logger.Debug(spf("Kill commands set to those of the project %s.", name))
		environ = append(environ, os.Environ()...) // add add the current environment so as to override the configuration
		// run the main commands
		logger.Debug(spf("Environment for project %s is: %v", name, environ))
		err = runCommands(p.Run, environ)
	}
	return err
}

func checkTriggers(project string) {
	// checking the seleted project for its triggers
	logger.Info(spf("Checking project %s... ", project))
	// get the project configuration
	p := new(Project)
	var meta map[string]string
	if err := conf.UnmarshalKey("projects."+project, &p); err != nil {
		logger.Error(spf("Error understanding project %s in %s: %s.", project, conf.ConfigFileUsed(), err))
		logger.Error(spf("Not running any trigger(s) for %s.", project))
	} else { // run the trigger
		for index, t := range p.Triggers {
			trigger, value, _ := strings.Cut(t, "=")
			trigger = strings.TrimSpace(strings.ToLower(trigger)) // normalize the trigger
			switch trigger {
			case "always":
				logger.Info(spf("Running %s for %s.", trigger, project))
			case "release":
				// get the latest value
				logger.Debug(spf("Getting latest release for %s...", project))
				if meta, err = evaluateRelease(value); err != nil {
					if strings.Contains(err.Error(), "rate limit") || strings.Contains(err.Error(), "abuse detection") {
						logger.Error(spf("You are being limited blocked by the GH API with error %s. Sleeping for %s...", err.Error(), defaultSleepMinutes.String()))
						time.Sleep(defaultSleepMinutes) // sleep for defaultSleepMinutes minutes
					} else {
						logger.Error(spf("Error getting latest %s for %s: %s", trigger, project, err.Error()))
					}
					continue
				} else {
					if meta["runIt"] == "false" {
						logger.Info(spf("Not running %s for %s because a newer release was not found.", trigger, project))
						continue
					}
				}
			default:
				logger.Error(spf("ignored unknown trigger type %s", trigger))
				continue
			}
			if err = runProject(p, project, &meta); err != nil {
				logger.Error(spf("Error running trigger %s for project %s: %s", trigger, project, err.Error()))
			} else {
				logger.Info(spf("Ran trigger called %s for %s successfully.", trigger, project))
				switch trigger {
				case "always": // nothing to do
				case "release":
					// replace the existing entry from triggers
					p.Triggers[index] = spf("release=%s/%s/%s", meta["owner"], meta["repo"], meta["new"])
				}
				// write the new configuration
				conf.Set("projects."+project, p)
				logger.Debug(spf("Updated triggers for %s.", project))
				logger.Info(spf("Ran trigger called %s for %s successfully.", trigger, project))
			}
		}
	}
}

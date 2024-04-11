/*
BSD 3-Clause License

Copyright (c) 2024, Shashank S. Harivyasi
All rights reserved.

Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

 1. Redistributions of source code must retain the above copyright notice,
    this list of conditions and the following disclaimer.

 2. Redistributions in binary form must reproduce the above copyright notice,
    this list of conditions and the following disclaimer in the documentation
    and/or other materials provided with the distribution.

 3. Neither the name of the copyright holder nor the names of its contributors
    may be used to endorse or promote products derived from this software
    without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
*/
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chigopher/pathlib"
	api_gh "github.com/google/go-github/github"
	"github.com/spf13/viper"
)

const (
	// defaultLogFile is the default log filename, can be changed in the configuration file
	defaultLogFile = "actwo.log"
	// defaultConfigFile is the default configuration filename, can be changed using the -config flag
	defaultConfigFile = "actwo.yml"
	// version for config file that is created by this version of the application
	confVersion = "1.0"
	// defaultSleepMinutes is the default sleep duration in minutes
	defaultSleepMinutes = 5 * time.Minute
)

var (
	// version is the application version, can be set at compile time
	version string = "0.1"
	// conf is the configuration object
	conf viper.Viper = *viper.New()
	// workDir is the working directory, default is the current directory
	workDir pathlib.Path = *pathlib.NewPath(".")
	// slog is the structured logger
	logger slog.Logger
	// sigs is the signal channel
	sigs chan os.Signal
	// the project that is currently selected
	project string
	// is the build process for the project running
	running bool
	// client for the github API
	client *api_gh.Client = api_gh.NewClient(nil)
)

/*
this is responsible for initializing the application by:
* 1. parsing command line arguments
* 2. reading the configuration file
* 3. setting up the log file
* 4. locking the configuration file
* 5. setting up signal handling
* 6. starting the main loop
*/
func main() {
	// Parse command line arguments
	setup := flag.Bool("setup", false, "Setup the configuration file")
	debug := flag.Bool("debug", false, "Debug mode")
	configFile := flag.String("config", defaultConfigFile, "Configuration filename")
	unlock := flag.Bool("unlock", false, "Forcefully unlock the configuration file")
	flag.Parse()
	// Initial logging to stdout, logging to file is started later as per configuration
	logger = *slog.New(slog.NewTextHandler(os.Stdout, nil))
	// Check if the configuration file needs to be setup
	conf.SetConfigFile(*configFile)
	if !*setup {
		// Attempt to read the configuration file
		if err := conf.ReadInConfig(); err != nil {
			// Check if the configuration file exists
			_, errExist := os.Stat(conf.ConfigFileUsed())
			if errExist != nil {
				logger.Error(spf("Configuration file (%s) not found. Did you create one?", conf.ConfigFileUsed()))
				logger.Info(spf("You can use the -setup flag to create a new configuration file. Exiting."))
			} else {
				logger.Error(spf("Error reading configuration file (%s): %s. Exiting.", conf.ConfigFileUsed(), err.Error()))
			}
			os.Exit(3) // 3 is the exit code when configuration file is not found
		} else {
			// Create a structured logger
			level := slog.LevelInfo
			if *debug {
				level = slog.LevelDebug
			}
			// Attempt to setup the log file
			if !conf.IsSet("settings.logfile") {
				logger.Error(spf("logfilename not defined. Check for `settings.logfile` in the configuration file. Exiting."))
				os.Exit(120) // 120 is the exit code when log filename is not set in the configuration file
			} else {
				if l, err := workDir.Join(conf.GetString("settings.logfile")).OpenFile(os.O_WRONLY | os.O_CREATE | os.O_APPEND); err != nil {
					logger.Error(spf("error opening log file: %s. Exiting.", err.Error()))
					os.Exit(124) // 124 is the exit code when log file cannot be opened
				} else {
					// change the logger so as to write to the log file
					logger = *slog.New(slog.NewJSONHandler(l, &slog.HandlerOptions{Level: level}))
					logger.Info(spf("Version %s of application started. PID is %d.", version, os.Getpid()))
					logger.Debug(spf("Log file %s opened successfully.", l.Name()))
					// check if the configuration file is locked
					if *unlock { // unlock the configuration file and exit
						unlockConfig()
					}
					logger.Debug(spf("Configuration file (%s) opened successfully.", conf.ConfigFileUsed()))
					// Lock the configuration file
					if err := lockConfig(os.Getpid()); err != nil {
						logger.Error(fmt.Sprintf("Error locking configuration file (%s): %s. Exiting.", conf.ConfigFileUsed(), err.Error()))
						os.Exit(211) // 211 is the exit code when locking fails
					} else {
						logger.Debug(fmt.Sprintf("Configuration file (%s) locked successfully.", conf.ConfigFileUsed()))
						// Set up signal handling
						sigs = make(chan os.Signal, 1)
						signal.Notify(sigs, os.Interrupt, syscall.SIGHUP)
						go func() {
							for { // empty sigs infinitely
								s := <-sigs
								if s == syscall.SIGHUP {
									logger.Info(fmt.Sprintf("Ignoring %s signal. PID is %d.", s.String(), os.Getpid()))
									continue
								} else {
									logger.Info(fmt.Sprintf("Received %s signal. Shutting down.", s.String()))
									shutdown()
								}
							}
						}()
						logger.Debug("Signal handling set up successfully.")
						// Read the sleepMinutes from the configuration file
						sleepMinutes := defaultSleepMinutes
						if conf.IsSet("settings.sleepMinutes") {
							if sleepMinutes, err = time.ParseDuration(spf("%fm", conf.GetFloat64("settings.sleepMinutes"))); err != nil {
								logger.Error(spf("Error parsing sleepMinutes: %s. Exiting.", err.Error()))
								os.Exit(19) // 19 is the exit code when sleepMinutes cannot be parsed
							}
						}
						// Start the main loop
						projects := getSubHeadings(&conf, "projects")
						if len(projects) == 0 {
							logger.Error("No projects defined in configuration file. Exiting.")
							os.Exit(404) // 404 is the exit code when no projects are defined
						} else {
							for { // infinite loop
								for _, project = range projects {
									checkProject()
								}
								// Sleep for sleepMinutes
								logger.Debug(spf("Sleeping for %s.", sleepMinutes.String()))
								time.Sleep(time.Duration(sleepMinutes))
							}
						}
					}
				}
			}

		}
	} else { // Setup the configuration file
		if err := func() (err error) {
			conf.Set("version", confVersion)
			conf.Set("settings.locked", 0)
			conf.Set("settings.sleepMinutes", defaultSleepMinutes.Minutes())
			conf.Set("settings.logfile", defaultLogFile)
			// Create a new configuration file
			err = conf.SafeWriteConfigAs(conf.ConfigFileUsed())
			return err
		}(); err != nil {
			logger.Error(spf("Error creating configuration file (%s): %s. Exiting.", conf.ConfigFileUsed(), err.Error()))
			os.Exit(30) // 30 is the exit code when configuration file cannot be created
		} else {
			logger.Info(spf("Configuration file (%s) created successfully.", conf.ConfigFileUsed()))
			shutdown()
		}
	}
}

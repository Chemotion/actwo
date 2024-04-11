package main

import (
	"fmt"
	"os"
	"syscall"

	"github.com/spf13/viper"
)

// shortcodes
var spf = fmt.Sprintf

// to manage config as loaded into Viper
func getSubHeadings(v *viper.Viper, key string) []string {
	subheadings := make([]string, len(v.GetStringMapString(key)))
	i := 0
	for k := range v.GetStringMapString(key) {
		subheadings[i] = k
		i++
	}
	return subheadings
}

// to lock and unlock configuration file
func lockConfig(proposed int) (err error) {
	current := conf.GetInt("settings.locked")
	if proposed != 0 && current != 0 && current != proposed {
		logger.Debug(spf("Previous lock was not unset properly (PID: %d).", current))
		var p *os.Process
		p, err = os.FindProcess(current) // On Unix systems, FindProcess always succeeds and returns a Process for the given pid, regardless of whether the process exists.
		if err == nil {
			if err = p.Signal(syscall.Signal(0)); err == nil {
				err = fmt.Errorf("process %d is still running", current)
			} else if err.Error() == "os: process already finished" {
				err = nil
			}
		}
	}
	if err != nil {
		err = fmt.Errorf("configuration file is cannot be locked by process %d because %s", current, err.Error())
	} else {
		conf.Set("settings.locked", proposed)
		if err = conf.WriteConfig(); err != nil {
			conf.Set("settings.locked", current) // revert to original value
		}
	}
	return err
}

// run the cleanup sequence
func cleanup() {
	cleanup := conf.GetStringSlice("projects." + project + ".cleanup")
	// run the cleanup commands
	for _, command := range cleanup {
		logger.Debug(spf("Running cleanup command: %s", command))
		if err := runCommand(command); err != nil {
			logger.Error(spf("Error running cleanup command %s: %s", command, err.Error()))
			break
		}
	}
}

// run the unlock sequence
func unlockConfig() {
	if ok := lockConfig(0); ok != nil {
		logger.Error(spf("Error unlocking configuration file (%s). Exiting.", conf.ConfigFileUsed()))
		os.Exit(212) // 212 is the exit code when unlocking fails
	} else {
		logger.Debug(spf("Configuration file (%s) unlocked successfully.", conf.ConfigFileUsed()))
		logger.Info("Exiting gracefully.")
		os.Exit(0) // unlock and exit
	}
}

// run the shutdown sequence
func shutdown() {
	if running {
		cleanup()
	}
	// unlock the configuration file and exit
	unlockConfig()
}

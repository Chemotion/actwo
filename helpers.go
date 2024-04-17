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
		var process *os.Process
		process, err = os.FindProcess(current) // On Unix systems, FindProcess always succeeds and returns a Process for the given pid, regardless of whether the process exists.
		if err == nil {
			if err = process.Signal(syscall.Signal(0)); err == nil {
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
	// stop any command if running
	if runner != nil && runner.ProcessState == nil {
		logger.Info(spf("Stopping running command: %s.", runner.String()))
		if err := runner.Process.Kill(); err != nil {
			logger.Error(spf("Error stopping running command: %s", err.Error()))
		}
		runCommands(kill, []string{}) // run the kill commands
	}
	// unlock the configuration file and exit
	unlockConfig()
}

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
func lockConfig(value int) (err error) {
	current := conf.GetInt("settings.locked")
	if value != 0 && current != value {
		logger.Debug(spf("Previous lock was not unset properly (PID: %d).", current))
		p, ok := os.FindProcess(current)
		if ok == nil {
			if err = p.Signal(syscall.Signal(0)); err == nil {
				return fmt.Errorf("configuration file is already locked by process %d", current)
			}
		}
	}
	conf.Set("settings.locked", value)
	if err = conf.WriteConfig(); err != nil {
		conf.Set("settings.locked", current) // revert to original value
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
			logger.Error(spf("Error running cleanup command %s: %s", command))
			break
		}
	}
}

// run the shutdown sequence
func shutdown() {
	if running {
		cleanup()
	}
	// unlock the configuration file
	if ok := lockConfig(0); ok != nil {
		logger.Error(spf("Error unlocking configuration file (%s). You may need to unlock it using the -unlock flag. Exiting.", conf.ConfigFileUsed()))
	} else {
		logger.Debug(spf("Configuration file (%s) unlocked successfully.", conf.ConfigFileUsed()))
	}
	os.Exit(0)
}

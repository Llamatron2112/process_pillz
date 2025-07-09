package main

import (
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v4/process"
)

// Sets the TuneD profile, using dbus
func (pm *PillManager) setTunedProfile(profile string) {
	obj := pm.dbusConn.Object("com.redhat.tuned", "/Tuned")

	// Getting the available profiles list from TuneD
	call := obj.Call("com.redhat.tuned.control.profiles", 0)
	if call.Err != nil {
		Logger.Errorf("failed to get profiles: %v", call.Err)
	}

	var allowedProfiles []string

	err := call.Store(&allowedProfiles)
	if err != nil {
		Logger.Errorf("failed to parse allowedProfiles: %v", err)
	}

	// If the profile required in the config file doesn't exist, output an error and do nothing
	if !slices.Contains(allowedProfiles, profile) {
		Logger.Errorf("TuneD profile %s is not available", profile)
		return
	}

	// Changing the profile
	err = obj.Call("com.redhat.tuned.control.switch_profile", 0, profile).Err
	if err != nil {
		Logger.Errorf("Error with a tuned dbus message: %v", err)
	} else {
		Logger.Infof("Tuned profile set to %s", profile)
	}
}

// Starts or stops schedulers
func (pm *PillManager) setScx(scx string) {
	obj := pm.dbusConn.Object("org.scx.Loader", "/org/scx/Loader")

	if scx == "none" {
		err := obj.Call("org.scx.Loader.StopScheduler", 0).Err
		if err != nil {
			Logger.Errorf("Error with scx_loader dbus message: %v", err)
		} else {
			Logger.Info("Scheduler stopped")
		}
	} else {
		args := strings.Split(scx, " ")

		sched := args[0]
		var mode uint
		if len(args) > 1 {
			i, err := strconv.Atoi(args[1])
			if err != nil {
				Logger.Errorf("Wrong scheduler mode %s using default (0)", args[1])
				mode = 0
			} else {
				mode = uint(i)
			}
		} else {
			mode = 0
		}

		err := obj.Call("org.scx.Loader.SwitchScheduler", 0, "scx_"+sched, mode).Err
		if err != nil {
			Logger.Errorf("Error with a scx_loader dbus message: %v", err)
		} else {
			Logger.Infof("Scheduler switched to %s", scx)
		}
	}
}

// Check a process and its parent, and determines if it is elligible to being reniced
func (pm *PillManager) reniceCheck(p *process.Process, nice int) {

	// Get cached process info if available
	procInfo, exists := pm.knownProcs[p.Pid]
	if !exists {
		Logger.Warnf("Process %d not found in cache during renice check", p.Pid)
		return
	}

	pParent, err := p.Parent()
	if err != nil {
		Logger.Warnf("Couldn't get the parent of %d : %v", p.Pid, err)
		return
	}

	// Check if parent has been reniced
	parentInfo, parentExists := pm.knownProcs[pParent.Pid]
	parentReniced := parentExists && parentInfo.Reniced

	// renicing the iterated proc, its sibling and chidren too, if a valid nice value is provided
	if parentReniced || pParent.Pid == pm.currentParent || p.Pid == pm.currentProc {
		err = syscall.Setpriority(syscall.PRIO_PROCESS, int(p.Pid), nice)
		if err != nil {
			Logger.Warnf("Couldn't change nice value of %s PID %d : %v", procInfo.Name, p.Pid, err)
			procInfo.Reniced = true
			return
		}

		// Mark process as reniced
		procInfo.Reniced = true
		Logger.Infof("reniced %s PID %d", procInfo.Name, p.Pid)
	}
}

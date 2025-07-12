package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/shirou/gopsutil/v4/process"
)

func (pm *PillManager) connectToDbus() error {
	maxRetries := 3
	timeBetweenRetries := 2 * time.Second
	if pm.dbusConn == nil {
		for i := range maxRetries {
			var err error
			pm.dbusConn, err = dbus.ConnectSystemBus()
			if err != nil {
				Logger.Errorf("Couldn't connect to dbus (try %d/%d) %v", i+1, maxRetries, err)
				time.Sleep(timeBetweenRetries)
			} else {
				Logger.Info("Connected to dbus")
				break
			}
		}
	}
	if pm.dbusConn == nil {
		return fmt.Errorf("Couldn't connect to dbus after %d tries.", maxRetries)
	} else {
		return nil
	}
}

// Sets the TuneD profile, using dbus
func (pm *PillManager) setTunedProfile(profile string) error {
	err := pm.connectToDbus()
	if err != nil {
		return fmt.Errorf("Failed to connect to dbus : %v", err)
	}

	obj := pm.dbusConn.Object("com.redhat.tuned", "/Tuned")
	if obj == nil {
		return fmt.Errorf("failed to connnect to TuneD. Is it running?")
	}

	validProfiles := obj.Call("com.redhat.tuned.control.profiles", 0).Body[0].([]string)
	if !slices.Contains(validProfiles, profile) {
		return fmt.Errorf("Invalid TuneD profile (%s)", profile)
	}

	return obj.Call("com.redhat.tuned.control.switch_profile", 0, profile).Err
}

// Change the SCX scheduler, using dbus
func (pm *PillManager) setScx(scx string) error {
	err := pm.connectToDbus()
	if err != nil {
		return fmt.Errorf("Failed to connect to dbus : %v", err)
	}

	obj := pm.dbusConn.Object("org.scx.Loader", "/org/scx/Loader")
	if obj == nil {
		return fmt.Errorf("Couldn't connect to scx_loader, is it running ?")
	}

	// If scheduler is set to none, stop any currently running scheduler
	if scx == "none" {
		return obj.Call("org.scx.Loader.StopScheduler", 0).Err
	}

	// Split the config string
	args := strings.Split(scx, " ")
	sched := args[0]

	// Checking if the scheduler is supported by scx_loader
	request, err := obj.GetProperty("org.scx.Loader.SupportedSchedulers")
	if err != nil {
		return fmt.Errorf("Couldn't get the list of schedulers from scx_loader")
	}

	supportedSchedulers := request.Value().([]string)
	if !slices.Contains(supportedSchedulers, sched) {
		return fmt.Errorf("Invalid scheduler (%s)", sched)
	}

	// Checking the scheduler mode if one is specified, if not use 0
	var mode uint
	if len(args) > 1 {
		i, err := strconv.Atoi(args[1])
		if err != nil || i < 0 || i > 4 {
			Logger.Errorf("Wrong scheduler mode %s using default (0)", args[1])
			mode = 0
		} else {
			mode = uint(i)
		}
	} else {
		mode = 0
	}

	// Executing the scheduler switch
	return obj.Call("org.scx.Loader.SwitchScheduler", 0, sched, mode).Err
}

// Check a process and its parent, and determines if it is elligible to being reniced
func (pm *PillManager) reniceCheck(p *process.Process, nice int) {

	// Get cached process info if available
	procInfo, exists := pm.knownProcs[p.Pid]
	if !exists {
		Logger.Warnf("Process %d not found in cache during renice check", p.Pid)
		return
	}

	if procInfo.Reniced {
		return
	}

	// Get parent process info
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
			Logger.Warnf("Couldn't change nice value of %s (PID %d) : %v", procInfo.Name, p.Pid, err)
			return
		}

		// Mark process as reniced
		procInfo.Reniced = true
		Logger.Infof("reniced %s (PID %d) to %d", procInfo.Name, p.Pid, nice)
	}
}

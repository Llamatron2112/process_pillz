package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/shirou/gopsutil/v4/process"
)

func (pm *PillManager) safeDBusCall(action func(*dbus.Conn) error) error {
	maxRetries := 3
	for attempt := range maxRetries {
		// Check if connection is nil
		if pm.dbusConn == nil {
			var err error
			pm.dbusConn, err = dbus.ConnectSystemBus()
			if err != nil {
				Logger.Errorf("D-Bus reconnection attempt %d failed: %v", attempt+1, err)
				time.Sleep(time.Second) // Small delay between retries
				continue
			}
		}

		// Attempt the action
		err := action(pm.dbusConn)
		if err == nil {
			return nil
		}

		// If action fails, potentially due to connection issue
		Logger.Errorf("D-Bus call failed (attempt %d): %v", attempt+1, err)
		pm.dbusConn.Close()
		pm.dbusConn = nil
	}

	return fmt.Errorf("failed to perform D-Bus action after %d attempts", maxRetries)
}

// Sets the TuneD profile, using dbus
func (pm *PillManager) setTunedProfile(profile string) {
	err := pm.safeDBusCall(func(conn *dbus.Conn) error {
		obj := conn.Object("com.redhat.tuned", "/Tuned")
		return obj.Call("com.redhat.tuned.control.switch_profile", 0, profile).Err
	})

	if err != nil {
		Logger.Errorf("Failed to set TuneD profile: %v", err)
	} else {
		Logger.Infof("Tuned profile set to %s", profile)
		return
	}
}

// Change the SCX scheduler, using dbus
func (pm *PillManager) setScx(scx string) {
	err := pm.safeDBusCall(func(conn *dbus.Conn) error {
		obj := conn.Object("org.scx.Loader", "/org/scx/Loader")

		if scx == "none" {
			return obj.Call("org.scx.Loader.StopScheduler", 0).Err
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

			return obj.Call("org.scx.Loader.SwitchScheduler", 0, "scx_"+sched, mode).Err
		}
	})

	if err != nil {
		Logger.Errorf("Failed to set SCX scheduler: %v", err)
	} else {
		if scx == "none" {
			Logger.Infof("SCX scheduler stopped")
		} else {
			Logger.Infof("SCX scheduler set to %s", scx)
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

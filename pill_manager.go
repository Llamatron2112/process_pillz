package main

import (
	"os/user"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/shirou/gopsutil/v4/process"
)

type ProcessInfo struct {
	Name     string
	Cmdline  string
	Username string
	Reniced  bool
}

// PillManager holds the state of the pill management system.
type PillManager struct {
	Triggers      map[string]string
	Pillz         map[string]map[string]string
	dbusConn      *dbus.Conn
	ticker        *time.Ticker
	scanInterval  time.Duration
	CurrentPill   string
	currentProc   int32
	currentParent int32
	userName      string                 // User running the daemon
	knownProcs    map[int32]*ProcessInfo // Cached process information
	currentScan   map[int32]bool         // Reused map for tracking current scan
}

var invalidParents = []string{"systemd", "srt-bwrap", "steam"}

// Function that returns the parent process, or the process itself if the parent was unusable
func (pm *PillManager) getValidParent(p *process.Process) int32 {
	pPar, err := p.Parent()
	if err != nil {
		Logger.Warnf("Couldn't find the parent of trigger process %d", p.Pid)
		return p.Pid
	}

	parName, err := pPar.Name()
	if err != nil {
		Logger.Warnf("Couldn't find the parent name %d", p.Pid)
		return p.Pid
	}

	if slices.Contains(invalidParents, parName) {
		Logger.Warnf("Invalid parent name %s", parName)
		return -1
	}

	return pPar.Pid
}

// The object storing the state of the pill manager
func NewPillManager(cfg Config) *PillManager {
	user, err := user.Current()
	if err != nil {
		Logger.Fatalf("Couldn't find the current user's name. %v", err)
	}

	// Setting the polling rate
	var scanInterval time.Duration
	if cfg.ScanInterval <= 0 {
		Logger.Warn("Error with scan_interval value. Using 3 seconds as sane default")
		scanInterval = 3 * time.Second
	} else {
		scanInterval = time.Duration(cfg.ScanInterval) * time.Second
	}

	ticker := time.NewTicker(scanInterval)

	return &PillManager{
		Triggers:      cfg.Triggers,
		Pillz:         cfg.Pills,
		dbusConn:      nil,
		ticker:        ticker,
		scanInterval:  scanInterval,
		CurrentPill:   "",
		currentProc:   0,
		currentParent: 0,
		userName:      user.Username,
		knownProcs:    make(map[int32]*ProcessInfo),
		currentScan:   make(map[int32]bool),
	}
}

func (pm *PillManager) checkTriggerMatch(cmd string) string {
	for trigger, pill := range pm.Triggers {
		if strings.Contains(cmd, trigger) {
			return pill
		}
	}
	return ""
}

// Look for a process matching one in the triggers list
func (pm *PillManager) scanProcesses() {
	// Fetching all the currently running processes
	processes, err := process.Processes()
	if err != nil {
		Logger.Errorf("Couldn't get running processes: %v", err)
		return
	}

	var shouldKeepCurrentPill bool
	var newPillToSwitch string
	var triggerProcess *process.Process

	current, err := process.NewProcess(pm.currentProc)
	if err != nil {
		shouldKeepCurrentPill = false
	} else {
		shouldKeepCurrentPill = true
		triggerProcess = current
	}

	// initialise global variables out of the loop
	curPill := pm.Pillz[pm.CurrentPill]

	// Getting nice config, and checking if a valid nice value is provided in the pill's config
	niceStr, isNice := curPill["nice"]

	// Clear and reuse the currentScan map
	for k := range pm.currentScan {
		delete(pm.currentScan, k)
	}

	var nice int

	if isNice && pm.CurrentPill != "default" {
		nice, err = strconv.Atoi(niceStr)
		if err != nil || nice < -20 || nice > 20 {
			Logger.Errorf("Invalid nice value in config: %s", niceStr)
			isNice = false
		}
	} else {
		isNice = false
	}

	// Run through the list of processes
	for _, p := range processes {
		// If the process has already been tested, use cached info
		procInfo, exists := pm.knownProcs[p.Pid]
		if !exists {
			pUser, err := p.Username()
			if err != nil {
				Logger.Warn("Can't get the user of a process : %v", err)
				continue
			}

			// Do not deal with non user processes
			if pUser != pm.userName {
				continue
			}

			pCmd, err := p.Cmdline()
			if err != nil {
				Logger.Warnf("Could not get command line of process %d", p.Pid)
			}

			pName, err := p.Name()
			if err != nil {
				Logger.Warnf("Could not get name of process %d", p.Pid)
				pName = "unknown"
			}

			// Create a new ProcessInfo and add it to the knownProcs map
			pm.knownProcs[p.Pid] = &ProcessInfo{
				Name:     pName,
				Cmdline:  pCmd,
				Username: pUser,
				Reniced:  false,
			}
			procInfo = pm.knownProcs[p.Pid]
		}
		// Store this process' PID in the list of processes seen during this scan
		pm.currentScan[p.Pid] = true

		if !shouldKeepCurrentPill {
			// Check if this cached process matches a trigger
			pillName := pm.checkTriggerMatch(procInfo.Cmdline)
			if pillName != "" {
				if pillName == pm.CurrentPill {
					shouldKeepCurrentPill = true
					triggerProcess = p
				} else {
					// Check if there is a pill with that name
					if _, pillExists := pm.Pillz[pillName]; pillExists {
						newPillToSwitch = pillName
						triggerProcess = p
					} else {
						Logger.Errorf("No pill named '%s'", pillName)
					}
				}
			}
		}

		// Do renice check if needed
		if isNice && !procInfo.Reniced {
			pm.reniceCheck(p, nice)
		}
	}

	// Removing missing processes from pm.knownProcs
	for pid := range pm.knownProcs {
		_, exists := pm.currentScan[pid]
		if !exists {
			delete(pm.knownProcs, pid)
		}
	}

	// Trigger and pills logic
	if !shouldKeepCurrentPill && pm.CurrentPill != "default" {
		pm.eatPill(nil, "default")

	} else if newPillToSwitch != "" && newPillToSwitch != pm.CurrentPill {
		pm.eatPill(triggerProcess, newPillToSwitch)

	} else if shouldKeepCurrentPill && triggerProcess.Pid != pm.currentProc {
		pm.currentProc = triggerProcess.Pid
		pm.currentParent = pm.getValidParent(triggerProcess)
		Logger.Infof("Changed trigger process to %d with parent %d", pm.currentProc, pm.currentParent)
	}
}

// Apply a profile
func (pm *PillManager) eatPill(p *process.Process, pillName string) {
	Logger.Infof("\033[1m[Eating %s pill]\033[0m", pillName)

	settings := pm.Pillz[pillName]

	for name, value := range settings {
		switch name {
		case "scx":
			err := pm.setScx(value)
			if err != nil {
				Logger.Errorf("Failed to change the scheduler : %v", err)
			} else {
				Logger.Infof("Scheduler set to %s", value)
			}

		case "tuned":
			err := pm.setTunedProfile(value)
			if err != nil {
				Logger.Errorf("Failed to set TuneD profile : %v", err)
			} else {
				Logger.Infof("TuneD profile set to %s", value)
			}

		case "nice":
			if pillName == "default" {
				Logger.Warn("Nice is not autorized in the default profile, ignoring")
			}

		default:
			Logger.Errorf("Unknown option: %s", name)
		}
	}

	// Reseting the known processes
	for _, procInfo := range pm.knownProcs {
		procInfo.Reniced = false
	}

	if p != nil {
		pm.currentProc = p.Pid
		pm.currentParent = pm.getValidParent(p)

	} else {
		pm.currentProc = 0
		pm.currentParent = 0
	}

	pm.CurrentPill = pillName

	Logger.Infof("current %d, parent %d", pm.currentProc, pm.currentParent)
}

func (pm *PillManager) Close() {
	if pm.dbusConn != nil {
		pm.dbusConn.Close()
	}
}

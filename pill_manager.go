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
	Cmdline  string
	Username string
	Reniced  bool
}

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

var invalidParents = []string{"systemd", "bash", "sh", "zsh", "fish"}

// The object storing all the data
func NewPillManager(cfg Config) *PillManager {

	db, err := dbus.ConnectSystemBus()
	if err != nil {
		Logger.Fatalf("Couldn't connect to dbus")
	}

	if db == nil {
		Logger.Fatal("Dbus connection is nil")
	}

	user, _ := user.Current()
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
		dbusConn:      db,
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

	shouldKeepCurrentPill := false

	// Track if we found a new trigger to switch to
	var newPillToSwitch string
	var triggerProcess *process.Process

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

		pUser, err := p.Username()
		if err != nil {
			Logger.Warn("Can't get the user of a process : %v", err)
			continue
		}

		// Do not deal with non user processes
		if pUser != pm.userName {
			continue
		}

		pPid := p.Pid

		// If the currently active process is still here, remember it has been found, and on to the next process
		if pPid == pm.currentProc {
			shouldKeepCurrentPill = true
		}

		// Store this process' PID in the list of processes seen during this scan
		pm.currentScan[pPid] = true

		// If the process has already been tested, use cached info
		procInfo, exists := pm.knownProcs[pPid]
		if !exists {
			pCmd, err := p.Cmdline()
			if err != nil {
				Logger.Warnf("Could not get command line of process %d", pPid)
			}

			pm.knownProcs[pPid] = &ProcessInfo{
				Cmdline:  pCmd,
				Username: pUser,
				Reniced:  false,
			}
			procInfo = pm.knownProcs[pPid]
		}

		// if exists {
		// Check if this cached process matches a trigger
		pillName := pm.checkTriggerMatch(procInfo.Cmdline)
		if pillName != "" {
			if pillName == pm.CurrentPill && !shouldKeepCurrentPill {
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
		pPar, err := triggerProcess.Parent()
		if err != nil {
			Logger.Warnf("Couldn't find the parent of trigger process %d", triggerProcess.Pid)
			pm.currentParent = triggerProcess.Pid
		} else {
			pm.currentParent = pPar.Pid
		}
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
			pm.setScx(value)
		case "tuned":
			pm.setTunedProfile(value)
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

		// Getting a proper parent, in case the parent is invalid process is its own parent
		newParent, err := p.Parent()
		if err != nil {
			pm.currentParent = p.Pid
		}

		newParentName, err := newParent.Name()
		if err != nil {
			pm.currentParent = p.Pid
		}

		if slices.Contains(invalidParents, newParentName) {
			pm.currentParent = p.Pid
		}

		if pm.currentParent != p.Pid {
			pm.currentParent = newParent.Pid
		}
	} else {
		pm.currentProc = 0
		pm.currentParent = 0
	}

	pm.CurrentPill = pillName

	// Perform an immediate scan after applying the pill
	// pm.scanProcesses()

	// Reset the ticker so next scan is at given interval
	// pm.ticker.Reset(pm.scanInterval)
}

func (pm *PillManager) Close() {
	if pm.dbusConn != nil {
		pm.dbusConn.Close()
	}
}

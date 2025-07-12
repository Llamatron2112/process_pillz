# Process Pillz

**Process Pillz** is a Linux daemon that automatically switches system performance profiles based on running processes. It monitors for specific processes (like games or applications) and applies optimized system configurations including CPU schedulers, TuneD profiles, and process nice values.

## Features

- **Automatic Profile Switching**: Detects running processes and switches to appropriate performance profiles
- **SCX Scheduler Support**: Integrates with [Sched-ext](https://github.com/sched-ext/scx) schedulers for advanced CPU scheduling
- **TuneD Integration**: Automatically switches TuneD profiles for system optimization
- **Process Nice Management**: Applies nice values to processes and their children for priority management
- **Systemd Integration**: Includes user service files for automatic startup and

## Requirements

### System Dependencies
- **Linux** with systemd (user services)
- **D-Bus** system bus access
- **Go 1.24+** (for building from source)

### Components
- **[Sched-ext (SCX)](https://github.com/sched-ext/scx)** - For advanced CPU scheduling
- **[TuneD](https://tuned-project.org/)** - For system performance tuning
- **Appropriate permissions** for changing process nice values

## Installation

### From Source

1. **Clone the repository:**
   ```bash
   git clone <repository-url>
   cd process_pillz
   ```

2. **Build and install:**
   ```bash
   make
   sudo make install
   ```

3. **Enable and start the service:**
   ```bash
   systemctl --user daemon-reload
   systemctl --user enable process_pillz
   systemctl --user start process_pillz
   ```

## Configuration

Process Pillz searches for configuration files in the following order:

1. `~/.config/process_pillz.yaml`
2. `~/.config/process_pillz/config.yaml`
3. `/etc/process_pillz/config.yaml`
4. `/usr/share/process_pillz/process_pillz.yaml.example`

### Configuration Options

#### Global Settings
- `scan_interval`: Time between process scans (seconds)

#### Triggers
- Key-value pairs where the key is a substring to match in process command lines
- Value is the name of the profile (pill) to activate

#### Pills (Profiles)
Each profile can contain:

- **`scx`**: SCX scheduler to use
  - Format: `scheduler_name [mode]`
  - Mode: 0=Auto, 1=Gaming, 2=PowerSave, 3=LowLatency, 4=Server
  - Use `none` to disable SCX scheduling

- **`tuned`**: TuneD profile name to activate

- **`nice`**: Nice value (-20 to 20) to apply to trigger process and children
  - Lower values = higher priority
  - Not allowed in `default` profile for safety

- **`blacklist`**: Processes that will never be reniced, designated by their executable name

## Usage

### Service Management

```bash
# Start the service
systemctl --user start process_pillz

# Stop the service
systemctl --user stop process_pillz

# Check status
systemctl --user status process_pillz

# View logs
journalctl --user -u process_pillz -f
```

### Process Nice Values

To allow changing process nice values, add your user to a group with appropriate permissions or configure sudo:

```bash
# Option 1: Add to games group (if using gaming features)
sudo usermod -a -G games $USER

# Option 2: Configure sudo for renice (more granular)
echo "$USER ALL=(root) NOPASSWD: /usr/bin/renice" | sudo tee /etc/sudoers.d/process_pillz
```

### D-Bus Permissions

Process Pillz requires access to the system D-Bus for TuneD and SCX integration. This usually works out of the box, but if you encounter permission issues:

```bash
# Check D-Bus access
dbus-send --system --print-reply --dest=com.redhat.tuned /Tuned com.redhat.tuned.control.profiles
```

## Troubleshooting

### Common Issues

**Config file not found:**
```bash
# Copy example config
cp /usr/share/process_pillz/process_pillz.yaml.example ~/.config/process_pillz.yaml
# Edit as needed
```

**Permission denied for config file:**
```bash
# Fix ownership and permissions
chown $USER ~/.config/process_pillz.yaml
chmod 644 ~/.config/process_pillz.yaml
```

**SCX scheduler not working:**
- Ensure SCX is installed and enabled in your kernel
- Check that the scheduler exists: `ls /sys/kernel/sched_ext/`
- Verify D-Bus access to SCX loader

**TuneD profile not switching:**
- Ensure TuneD is installed and running: `systemctl status tuned`
- Check available profiles: `tuned-adm list`
- Verify profile exists: `tuned-adm active`

**Process nice changes not working:**
- Check permissions (see Permissions Setup above)
- Verify the process is owned by your user
- Check logs for specific error messages

### Debugging

**View detailed logs:**
```bash
journalctl --user -u process_pillz -f --no-pager
```

**Test configuration:**
```bash
# Validate config syntax (planned feature)
process_pillz --validate-config
```

**Check current status:**
```bash
# Show current profile and active triggers (planned feature)
process_pillz --status
```

## Build Information

```bash
# Show version and build info
make version

# Build targets
make help
```

## Contributing

This project is in beta. When reporting issues, please include:

- Your Linux distribution and kernel version
- SCX and TuneD versions (if applicable)
- Process Pillz version (`make version`)
- Configuration file (redacted as needed)
- Relevant log output

**Process Pillz** - Automatic performance profile switching for Linux gaming and productivity.

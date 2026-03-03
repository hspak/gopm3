# gopm3
Dumb Process Manager

## Config
Currently hardcoded to look for a JSON file, `gopm3.config.json` in the current
directory.

Config Format
```json
[
    {
        "name": "some name",            // The label to reference the command by
        "command": "ls",                // The command to run
        "args": ["-a", "-b"],           // The arguments to pass to the command
        "restart_delay": 1000,          // Delay (ms) before each restart
        "docker_managed": false,        // (Optional) Mark true when this process starts a docker container
        "use_process_group": true,      // (Optional) Send signals to the command process group
        "disable_logs": false           // (Optional) Disable TUI log streaming for this process
    },
    {
        ...
    },
    ...
]
```

## Usage
- Arrow keys to navigate between processes
- Mouse clicks to focus the different panes
- `<Space>` to restart highlighted process
- `m` to toggle mouse mode (default: on, text is only highlightable in non-mouse mode)
- `ESC` or `Ctrl + c` to exit
- All logs (both stdout/stderr) are replicated to `~/.gopm3/<process-name>.log`

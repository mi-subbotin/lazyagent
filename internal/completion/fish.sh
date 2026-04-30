# fish completion for lazyagent.

complete -c lazyagent -f

# Top-level subcommands.
complete -c lazyagent -n "__fish_use_subcommand" -a "config"     -d "Manage ~/.lazyagent/config.toml"
complete -c lazyagent -n "__fish_use_subcommand" -a "logs"       -d "Inspect the log file"
complete -c lazyagent -n "__fish_use_subcommand" -a "shared"     -d "Initialise the shared store"
complete -c lazyagent -n "__fish_use_subcommand" -a "completion" -d "Print a shell completion script"

# Global flags (apply when no subcommand is being typed).
complete -c lazyagent -n "__fish_use_subcommand" -l mock       -d "Use the mock data source"
complete -c lazyagent -n "__fish_use_subcommand" -l version    -d "Print version and exit"
complete -c lazyagent -n "__fish_use_subcommand" -l verbose -s v -d "Debug log level"
complete -c lazyagent -n "__fish_use_subcommand" -l log-file   -d "Override log file path" -r -F
complete -c lazyagent -n "__fish_use_subcommand" -l log-format -d "Log format" -xa "text json"

# config verbs.
complete -c lazyagent -n "__fish_seen_subcommand_from config; and not __fish_seen_subcommand_from init show edit validate" \
    -a "init show edit validate"
complete -c lazyagent -n "__fish_seen_subcommand_from config; and __fish_seen_subcommand_from init" \
    -l force -d "Overwrite an existing config file"
complete -c lazyagent -n "__fish_seen_subcommand_from config; and __fish_seen_subcommand_from validate" \
    -l path -d "Config file to check" -r -F

# logs verbs.
complete -c lazyagent -n "__fish_seen_subcommand_from logs; and not __fish_seen_subcommand_from path tail clean" \
    -a "path tail clean"
complete -c lazyagent -n "__fish_seen_subcommand_from logs; and __fish_seen_subcommand_from tail" \
    -s n -d "Number of trailing lines"

# shared verbs.
complete -c lazyagent -n "__fish_seen_subcommand_from shared; and not __fish_seen_subcommand_from init" \
    -a "init"

# completion shells.
complete -c lazyagent -n "__fish_seen_subcommand_from completion; and not __fish_seen_subcommand_from bash zsh fish" \
    -a "bash zsh fish"

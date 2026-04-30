#compdef lazyagent
# zsh completion for lazyagent.

_lazyagent() {
    local -a subcommands global_flags
    subcommands=(
        'config:Manage ~/.lazyagent/config.toml'
        'logs:Inspect the log file'
        'shared:Initialise the shared store'
        'completion:Print a shell completion script'
    )
    global_flags=(
        '--mock[use the mock data source]'
        '--version[print version and exit]'
        '--verbose[debug log level]'
        '-v[debug log level]'
        '--log-file[override log file path]:file:_files'
        '--log-format[log format]:format:(text json)'
        '--help[show help]'
        '-h[show help]'
    )

    local context state line
    _arguments -C \
        '1: :->subcmd' \
        '*::arg:->args' && return 0

    case $state in
        subcmd)
            _describe -t subcommands 'subcommand' subcommands
            _values 'flag' $global_flags
            ;;
        args)
            case $line[1] in
                config)
                    if (( CURRENT == 2 )); then
                        _values 'config verb' \
                            'init[seed defaults at ~/.lazyagent/config.toml]' \
                            'show[print effective config]' \
                            'edit[open config in $EDITOR]' \
                            'validate[check the config file]'
                    else
                        case $line[2] in
                            init)     _values 'flag' '--force[overwrite an existing config file]' ;;
                            validate) _arguments '--path[config file to check]:file:_files' ;;
                        esac
                    fi
                    ;;
                logs)
                    if (( CURRENT == 2 )); then
                        _values 'logs verb' \
                            'path[print resolved log file path]' \
                            'tail[print last N lines of active log]' \
                            'clean[remove active log + rotated siblings]'
                    else
                        case $line[2] in
                            tail) _arguments '-n[number of trailing lines]:int:' ;;
                        esac
                    fi
                    ;;
                shared)
                    _values 'shared verb' 'init[initialise the shared store]'
                    ;;
                completion)
                    _values 'shell' bash zsh fish
                    ;;
            esac
            ;;
    esac
}

if [ "${funcstack[1]-}" = "_lazyagent" ]; then
    _lazyagent "$@"
else
    compdef _lazyagent lazyagent
fi

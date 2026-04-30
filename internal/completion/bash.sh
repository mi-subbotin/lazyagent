# shellcheck shell=bash
# bash completion for lazyagent.

_lazyagent() {
    local cur prev words cword
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    words=("${COMP_WORDS[@]}")
    cword="${COMP_CWORD}"

    local subcommands="config logs shared completion"
    local global_flags="--mock --version --verbose -v --log-file --log-format -h --help"

    case "${prev}" in
        --log-file)
            COMPREPLY=( $(compgen -f -- "${cur}") )
            return 0
            ;;
        --log-format)
            COMPREPLY=( $(compgen -W "text json" -- "${cur}") )
            return 0
            ;;
    esac

    if [ "${cword}" -eq 1 ]; then
        COMPREPLY=( $(compgen -W "${subcommands} ${global_flags}" -- "${cur}") )
        return 0
    fi

    case "${words[1]}" in
        config)
            if [ "${cword}" -eq 2 ]; then
                COMPREPLY=( $(compgen -W "init show edit validate" -- "${cur}") )
                return 0
            fi
            case "${words[2]}" in
                init)     COMPREPLY=( $(compgen -W "--force" -- "${cur}") ) ;;
                validate) COMPREPLY=( $(compgen -W "--path" -- "${cur}") ) ;;
            esac
            ;;
        logs)
            if [ "${cword}" -eq 2 ]; then
                COMPREPLY=( $(compgen -W "path tail clean" -- "${cur}") )
                return 0
            fi
            case "${words[2]}" in
                tail) COMPREPLY=( $(compgen -W "-n" -- "${cur}") ) ;;
            esac
            ;;
        shared)
            if [ "${cword}" -eq 2 ]; then
                COMPREPLY=( $(compgen -W "init" -- "${cur}") )
            fi
            ;;
        completion)
            if [ "${cword}" -eq 2 ]; then
                COMPREPLY=( $(compgen -W "bash zsh fish" -- "${cur}") )
            fi
            ;;
    esac
    return 0
}

complete -F _lazyagent lazyagent

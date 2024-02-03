# mirrorbits(1) completion                                 -*- shell-script -*-
# ex: ts=4 sw=4 et filetype=sh
#
# Copyright (c) 2024 Arnaud Rebillout <arnaudr@kali.org>
# Distributed under the same license as mirrorbits.

_in_array() {
    local i
    for i in "${@:2}"; do
        [[ $1 = "$i" ]] && return
    done
}

_mirrorbits_list() {
    local port=$1
    mirrorbits -p $port list -state=false | tail -n +2 || :
}

_mirrorbits() {
    # Variables assigned by _init_completion:
    #   cur    Current argument.
    #   prev   Previous argument.
    #   words  Argument array.
    #   cword  Argument array size.
    local cur prev words cword
    _init_completion || return

    # Mirrorbits cli options, must come before the command
    local CLI_OPTIONS=(
        "-P"
        "-a"
        "-cpuprofile"
        "-debug"
        "-h"
        "-p")

    # Mirrorbits commands
    local COMMANDS=(
        "add"
        "daemon"
        "disable"
        "edit"
        "enable"
        "export"
        "geoupdate"
        "list"
        "logs"
        "refresh"
        "reload"
        "remove"
        "scan"
        "show"
        "stats"
        "upgrade"
        "version")

    # Check if a command was already given
    local command i
    for (( i = 1; i < cword; i++ )); do
        if _in_array "${words[i]}" "${COMMANDS[@]}"; then
            command=${words[i]}
            break
        fi
    done

    # Check if a port was already given, tricky as we must
    # support -p X, -p=X, --p X and --p=X
    local port
    for (( i = 1; i < cword; i++ )); do
        if [[ ! ${words[i]} =~ ^[0-9]+$ ]]; then
            continue
        fi
        if [[ ${words[i - 1]} =~ ^-?-p$ ]]; then
            port=${words[i]}
            break
        fi
        if [[ ${words[i - 1]} == = ]] && (( i > 1 )) && [[ ${words[i - 2]} =~ ^-?-p$ ]]; then
            port=${words[i]}
            break
        fi
    done

    # No port? Default port is 3390
    if [[ -z $port ]]; then
        port=3390
    fi

    # Completion per command
    if [[ -n $command ]]; then
        case $command in
            daemon)
                COMPREPLY=( $( compgen -W '-help -config -cpuprofile
                    -debug -log -monitor -p' -- "$cur" ) )
                ;;
            add)
                COMPREPLY=( $( compgen -W '-help -admin-email -admin-name
                    -as-only -comment -continent-only -country-only
                    -custom-data -ftp -http -rsync -score
                    -sponsor-logo -sponsor-name -sponsor-url
                    ' -- "$cur" ) )
                ;;
            disable|edit|enable|show)
                case $cur in
                    -*)
                        COMPREPLY=( $( compgen -W '-help' -- "$cur" ) )
                        ;;
                    *)
                        COMPREPLY=( $( compgen -W "$( _mirrorbits_list $port )" -- "$cur" ) )
                        ;;
                esac
                ;;
            export)
                COMPREPLY=( $( compgen -W '-help -disabled -ftp
                    -http -rsync' -- "$cur" ) )
                ;;
            list)
                COMPREPLY=( $( compgen -W '-help -disabled -down -enabled
                    -ftp -http -location -rsync -score -state
                    ' -- "$cur" ) )
                ;;
            logs)
                case $cur in
                    -*)
                        COMPREPLY=( $( compgen -W '-help -l' -- "$cur" ) )
                        ;;
                    *)
                        COMPREPLY=( $( compgen -W "$( _mirrorbits_list $port )" -- "$cur" ) )
                        ;;
                esac
                ;;
            refresh)
                COMPREPLY=( $( compgen -W '-help -rehash' -- "$cur" ) )
                ;;
            reload|upgrade|version)
                COMPREPLY=( $( compgen -W '-help' -- "$cur" ) )
                ;;
            remove)
                case $cur in
                    -*)
                        COMPREPLY=( $( compgen -W '-help -f' -- "$cur" ) )
                        ;;
                    *)
                        COMPREPLY=( $( compgen -W "$( _mirrorbits_list $port )" -- "$cur" ) )
                        ;;
                esac
                ;;
            scan)
                case $cur in
                    -*)
                        COMPREPLY=( $( compgen -W '-help -all -enable -ftp
                            -rsync -timeout' -- "$cur" ) )
                        ;;
                    *)
                        COMPREPLY=( $( compgen -W "$( _mirrorbits_list $port )" -- "$cur" ) )
                        ;;
                esac
                ;;
            stats)
                case $cur in
                    -*)
                        COMPREPLY=( $( compgen -W '-help -end-date -h -start-date
                            ' -- "$cur" ) )
                        ;;
                    *)
                        if _in_array mirror "${words[@]:2}"; then
                            COMPREPLY=( $( compgen -W "$( _mirrorbits_list $port )" -- "$cur" ) )
                        elif _in_array file "${words[@]:2}"; then
                            COMPREPLY=()
                        else
                            COMPREPLY=( $( compgen -W 'file mirror' -- "$cur" ) )
                        fi
                        ;;
                esac
                ;;
            *)
                COMPREPLY=()
                ;;
        esac
    else  # no command yet
        case "$cur" in
            -*)
                COMPREPLY=( $( compgen -W '${CLI_OPTIONS[@]}' -- "$cur" ) )
                ;;
            *)
                COMPREPLY=( $( compgen -W '${COMMANDS[@]}' -- "$cur" ) )
                ;;
        esac
    fi

    return 0
} &&
complete -F _mirrorbits mirrorbits

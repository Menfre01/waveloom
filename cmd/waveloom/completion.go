package main

import (
	"fmt"
	"os"
)

// completionScripts 存储各 shell 的补全脚本内容。
var completionScripts = map[string]string{
	"bash": bashCompletion,
	"zsh":  zshCompletion,
	"fish": fishCompletion,
}

// runCompletion 输出指定 shell 的补全脚本。
func runCompletion(shell string) {
	script, ok := completionScripts[shell]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unsupported shell: %s (supported: bash, zsh, fish)\n", shell)
		os.Exit(1)
	}
	fmt.Print(script)
}

const bashCompletion = `# waveloom bash completion
# Source this file in your .bashrc:
#   source <(waveloom completion bash)
# Or copy to /usr/local/etc/bash_completion.d/

_waveloom() {
    local cur prev opts
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "$prev" in
        waveloom)
            opts="setup ls mcp completion --model --provider --system-prompt --max-turns --context-limit --theme --locale --log-level --bypass-permissions --tool-timeout --resume --continue --settings --version --help"
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
            return 0
            ;;
        --model)
            opts="deepseek-v4-pro deepseek-v4-flash gpt-4o"
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
            return 0
            ;;
        --provider)
            opts="kimi deepseek openai"
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
            return 0
            ;;
        --theme)
            opts="auto dark light darkcolorblind lightcolorblind"
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
            return 0
            ;;
        completion)
            opts="bash zsh fish"
            COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
            return 0
            ;;
        *)
            ;;
    esac
    opts="setup ls mcp completion --model --provider --system-prompt --max-turns --context-limit --theme --locale --log-level --bypass-permissions --tool-timeout --resume --continue --settings --version --help"
    COMPREPLY=( $(compgen -W "$opts" -- "$cur") )
    return 0
}

complete -F _waveloom waveloom
`

const zshCompletion = `#compdef waveloom

_waveloom() {
    local -a commands
    commands=(
        'setup:首次配置向导'
        'ls:列出最近会话'
        'mcp:MCP Server 管理'
        'completion:输出 shell 补全脚本'
    )

    local -a flags
    flags=(
        '--model[指定模型名]:model:(deepseek-v4-pro deepseek-v4-flash gpt-4o)'
        '--provider[LLM Provider]:provider:(kimi deepseek openai)'
        '--system-prompt[自定义系统提示词]:prompt'
        '--max-turns[最大轮数，0不限制]:turns'
        '--context-limit[上下文窗口大小]:limit'
        '--theme[主题]:theme:(auto dark light darkcolorblind lightcolorblind)'
        '--locale[界面语言]:locale:(auto zh-CN en-US)'
        '--log-level[日志级别]:level:(debug info warn error)'
        '--bypass-permissions[跳过权限检查]'
        '--tool-timeout[单工具执行超时]:duration'
        '--resume[恢复指定会话]:session_id'
        '--continue[恢复最近会话]'
        '--settings[指定配置文件路径]:path:_files'
        '--version[显示版本号]'
        '--help[显示帮助]'
    )

    _arguments -s \
        '1: :_waveloom_commands' \
        '*: :_waveloom_args' \
        $flags
}

_waveloom_commands() {
    local cmd=${words[1]}
    if [[ -n "$cmd" ]] && (( CURRENT == 1 )); then
        return
    fi
    _describe 'command' commands
}

_waveloom_args() {
    _arguments $flags
}

_waveloom "$@"
`

const fishCompletion = `# waveloom fish completion
# Copy to ~/.config/fish/completions/waveloom.fish

complete -c waveloom -f

# Commands
complete -c waveloom -n __fish_use_subcommand -a setup -d "首次配置向导"
complete -c waveloom -n __fish_use_subcommand -a ls -d "列出最近会话"
complete -c waveloom -n __fish_use_subcommand -a mcp -d "MCP Server 管理"
complete -c waveloom -n __fish_use_subcommand -a completion -d "输出 shell 补全脚本"

# Flags
complete -c waveloom -l model -d "指定模型名" -a "deepseek-v4-pro deepseek-v4-flash gpt-4o"
complete -c waveloom -l provider -d "LLM Provider" -a "kimi deepseek openai"
complete -c waveloom -l system-prompt -d "自定义系统提示词" -r
complete -c waveloom -l max-turns -d "最大轮数，0不限制" -r
complete -c waveloom -l context-limit -d "上下文窗口大小" -r
complete -c waveloom -l theme -d "主题" -a "auto dark light darkcolorblind lightcolorblind"
complete -c waveloom -l locale -d "界面语言" -a "auto zh-CN en-US"
complete -c waveloom -l log-level -d "日志级别" -a "debug info warn error"
complete -c waveloom -l bypass-permissions -d "跳过权限检查"
complete -c waveloom -l tool-timeout -d "单工具执行超时" -r
complete -c waveloom -l resume -d "恢复指定会话" -r
complete -c waveloom -l continue -d "恢复最近会话"
complete -c waveloom -l settings -d "指定配置文件路径" -r -F
complete -c waveloom -l version -d "显示版本号"
complete -c waveloom -l help -d "显示帮助"
`

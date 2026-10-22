package pcsclone

import (
	"fmt"
	"sort"
	"strings"
)

func completionCommand() *command {
	return &command{
		Name: "completion", Category: "Help",
		Summary: "Generate a shell completion script",
		Usage:   "pcsclone completion bash|zsh|powershell",
		Description: `Print a completion script for command names, subcommands and long
options. File paths are not completed. PowerShell completion requires
PowerShell 7 or newer.`,
		MinArgs: 1, MaxArgs: 1, NoRuntime: true,
		Run: runCompletion,
	}
}

func runCompletion(c *commandContext) error {
	script, err := completionScript(strings.ToLower(c.args[0]))
	if err != nil {
		return usageErrorf("%s", err)
	}
	fmt.Fprint(c.app.Stdout, script)
	return nil
}

func completionScript(shell string) (string, error) {
	cases := collectCompletionCases()
	switch shell {
	case "bash":
		return bashCompletion(cases), nil
	case "zsh":
		return zshCompletion(cases), nil
	case "powershell", "pwsh":
		return powershellCompletion(cases), nil
	default:
		return "", fmt.Errorf("unsupported shell %q; use bash, zsh or powershell", shell)
	}
}

func collectCompletionCases() map[string][]string {
	cases := map[string][]string{}
	top := make([]string, 0, len(topLevelCommands())+3)
	for _, cmd := range topLevelCommands() {
		top = append(top, cmd.Name)
	}
	top = append(top, "help", "version", "completion")
	cases[""] = append(append([]string(nil), top...), "--config", "-c", "--help", "-h", "--version", "-V")
	cases["help"] = top
	for _, cmd := range topLevelCommands() {
		walkCompletion(cmd, "", cases)
	}
	// walkCompletion sees completion as a leaf and would replace this entry
	// with its option list, so install the shell arguments afterwards.
	cases["completion"] = []string{"bash", "zsh", "powershell", "--help", "-h"}
	for key, values := range cases {
		sort.Strings(values)
		cases[key] = compactStrings(values)
	}
	return cases
}

func walkCompletion(cmd *command, prefix string, cases map[string][]string) {
	path := cmd.Name
	if prefix != "" {
		path = prefix + " " + cmd.Name
	}
	if cmd.Group != nil {
		names := make([]string, 0, len(cmd.Group))
		for _, sub := range cmd.Group {
			names = append(names, sub.Name)
		}
		cases[path] = append(append([]string(nil), names...), "--help", "-h")
		cases["help "+path] = names
		for _, sub := range cmd.Group {
			walkCompletion(sub, path, cases)
		}
		return
	}
	flags := make([]string, 0, len(cmd.Flags)+4)
	for _, spec := range cmd.Flags {
		flags = append(flags, "--"+spec.Name)
		if spec.Short != "" {
			flags = append(flags, "-"+spec.Short)
		}
	}
	flags = append(flags, "--help", "-h")
	cases[path] = flags
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func sortedCaseKeys(cases map[string][]string) []string {
	keys := make([]string, 0, len(cases))
	for key := range cases {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func bashCompletion(cases map[string][]string) string {
	var b strings.Builder
	b.WriteString("# bash completion for pcsclone\n")
	b.WriteString("_pcsclone_completion() {\n")
	b.WriteString("  local cur path i w candidates\n")
	b.WriteString("  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("  path=\"\"\n")
	b.WriteString("  i=1\n")
	b.WriteString("  while [ \"$i\" -lt \"$COMP_CWORD\" ]; do\n")
	b.WriteString("    w=\"${COMP_WORDS[$i]}\"\n")
	b.WriteString("    case \"$w\" in\n")
	b.WriteString("      --config|-c) i=$((i+2)); continue ;;\n")
	b.WriteString("      -*) i=$((i+1)); continue ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("    path=\"${path:+$path }$w\"\n")
	b.WriteString("    i=$((i+1))\n")
	b.WriteString("  done\n")
	b.WriteString("  candidates=\"\"\n")
	b.WriteString("  case \"$path\" in\n")
	for _, key := range sortedCaseKeys(cases) {
		fmt.Fprintf(&b, "    \"%s\") candidates=\"%s\" ;;\n", key, strings.Join(cases[key], " "))
	}
	b.WriteString("  esac\n")
	b.WriteString("  if [ -n \"$candidates\" ]; then\n")
	b.WriteString("    COMPREPLY=($(compgen -W \"$candidates\" -- \"$cur\"))\n")
	b.WriteString("  fi\n")
	b.WriteString("}\n")
	b.WriteString("complete -F _pcsclone_completion pcsclone\n")
	return b.String()
}

func zshCompletion(cases map[string][]string) string {
	var b strings.Builder
	b.WriteString("#compdef pcsclone\n")
	b.WriteString("_pcsclone_completion() {\n")
	b.WriteString("  local path=\"\" i=2 w\n")
	b.WriteString("  while (( i < CURRENT )); do\n")
	b.WriteString("    w=\"${words[i]}\"\n")
	b.WriteString("    case \"$w\" in\n")
	b.WriteString("      --config|-c) (( i += 2 )); continue ;;\n")
	b.WriteString("      -*) (( i++ )); continue ;;\n")
	b.WriteString("    esac\n")
	b.WriteString("    path=\"${path:+$path }$w\"\n")
	b.WriteString("    (( i++ ))\n")
	b.WriteString("  done\n")
	b.WriteString("  local -a candidates\n")
	b.WriteString("  case \"$path\" in\n")
	for _, key := range sortedCaseKeys(cases) {
		fmt.Fprintf(&b, "    \"%s\") candidates=(%s) ;;\n", key, quoteWords(cases[key]))
	}
	b.WriteString("  esac\n")
	b.WriteString("  if (( ${#candidates} )); then\n")
	b.WriteString("    _describe 'pcsclone' candidates\n")
	b.WriteString("  fi\n")
	b.WriteString("}\n")
	b.WriteString("compdef _pcsclone_completion pcsclone\n")
	return b.String()
}

func powershellCompletion(cases map[string][]string) string {
	var b strings.Builder
	b.WriteString("# PowerShell 7+ completion for pcsclone\n")
	b.WriteString("Register-ArgumentCompleter -Native -CommandName pcsclone -ScriptBlock {\n")
	b.WriteString("  param($wordToComplete, $commandAst, $cursorPosition)\n")
	b.WriteString("  $elements = @($commandAst.CommandElements | Select-Object -Skip 1)\n")
	b.WriteString("  if ($elements.Count -gt 0 -and $wordToComplete -ne '' -and $elements[-1].Value -eq $wordToComplete) {\n")
	b.WriteString("    if ($elements.Count -eq 1) { $elements = @() } else { $elements = @($elements[0..($elements.Count - 2)]) }\n")
	b.WriteString("  }\n")
	b.WriteString("  $pathParts = @()\n")
	b.WriteString("  for ($i = 0; $i -lt $elements.Count; $i++) {\n")
	b.WriteString("    $value = $elements[$i].Value\n")
	b.WriteString("    if ($value -eq '--config' -or $value -eq '-c') { $i++; continue }\n")
	b.WriteString("    if ($value.StartsWith('-')) { continue }\n")
	b.WriteString("    $pathParts += $value\n")
	b.WriteString("  }\n")
	b.WriteString("  $path = $pathParts -join ' '\n")
	b.WriteString("  $candidates = switch ($path) {\n")
	for _, key := range sortedCaseKeys(cases) {
		fmt.Fprintf(&b, "    '%s' { %s }\n", key, quoteWordsPowerShell(cases[key]))
	}
	b.WriteString("    default { @() }\n")
	b.WriteString("  }\n")
	b.WriteString("  $candidates | Where-Object { $_ -like \"$wordToComplete*\" } | ForEach-Object {\n")
	b.WriteString("    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)\n")
	b.WriteString("  }\n")
	b.WriteString("}\n")
	return b.String()
}

func quoteWords(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		quoted = append(quoted, "'"+strings.ReplaceAll(word, "'", `'\''`)+"'")
	}
	return strings.Join(quoted, " ")
}

func quoteWordsPowerShell(words []string) string {
	quoted := make([]string, 0, len(words))
	for _, word := range words {
		quoted = append(quoted, "'"+strings.ReplaceAll(word, "'", "''")+"'")
	}
	return "@(" + strings.Join(quoted, ",") + ")"
}

package storage

import "strings"

// Confluence panel macro -> canonical callout kind. The pairs are chosen so a
// Confluence -> canonical -> Confluence trip gives back the same macro:
// info (blue) = info, tip (green) = tip, note (yellow) = warning,
// warning (red) = danger.
var calloutFromMacro = map[string]string{
	"info":    "info",
	"tip":     "tip",
	"note":    "warning",
	"warning": "danger",
	"panel":   "note",
}

// macroFromCallout picks the Confluence panel for an Obsidian-style callout kind.
func macroFromCallout(kind string) string {
	switch strings.ToLower(kind) {
	case "tip", "hint", "important", "success", "check", "done":
		return "tip"
	case "warning", "caution", "attention":
		return "note"
	case "danger", "error", "failure", "fail", "missing", "bug":
		return "warning"
	default: // note, info, abstract, summary, tldr, todo, question, example, quote, ...
		return "info"
	}
}

// Code macro languages Confluence Data Center accepts, keyed by the names
// people use in Markdown fences.
var confluenceLang = map[string]string{
	"bash": "bash", "sh": "bash", "shell": "bash", "zsh": "bash", "console": "bash",
	"csharp": "c#", "cs": "c#", "c#": "c#",
	"c": "cpp", "cpp": "cpp", "c++": "cpp", "h": "cpp", "hpp": "cpp",
	"css": "css", "less": "css", "scss": "sass", "sass": "sass",
	"diff": "diff", "patch": "diff",
	"erlang": "erl", "erl": "erl",
	"groovy": "groovy", "gradle": "groovy",
	"java": "java", "javafx": "jfx", "jfx": "jfx",
	"javascript": "js", "js": "js", "jsx": "js", "typescript": "js", "ts": "js", "tsx": "js", "json": "js", "jsonc": "js",
	"php": "php", "perl": "perl", "pl": "perl",
	"powershell": "powershell", "ps1": "powershell", "ps": "powershell", "pwsh": "powershell",
	"python": "py", "py": "py", "ruby": "ruby", "rb": "ruby",
	"sql": "sql", "mysql": "sql", "postgresql": "sql", "plsql": "sql", "tsql": "sql",
	"scala": "scala", "vb": "vb", "vbnet": "vb", "vba": "vb",
	"xml": "xml", "html": "xml", "xhtml": "xml", "svg": "xml", "xaml": "xml",
	"yaml": "yml", "yml": "yml",
	"delphi": "delphi", "pascal": "delphi", "applescript": "applescript",
	"coldfusion": "coldfusion", "actionscript": "actionscript3", "actionscript3": "actionscript3",
}

// markdownLang names a Confluence code language the way Markdown fences do.
var markdownLang = map[string]string{
	"c#": "csharp", "js": "javascript", "py": "python", "yml": "yaml", "erl": "erlang",
	"vb": "vbnet", "actionscript3": "actionscript", "jfx": "javafx", "text": "", "none": "",
}

func langToConfluence(info string) string {
	return confluenceLang[strings.ToLower(strings.TrimSpace(info))]
}

func langFromConfluence(lang string) string {
	l := strings.ToLower(strings.TrimSpace(lang))
	if m, ok := markdownLang[l]; ok {
		return m
	}
	return l
}

// Confluence emoticon names -> emoji.
var emoticons = map[string]string{
	"smile": "🙂", "sad": "🙁", "cheeky": "😛", "laugh": "😀", "wink": "😉",
	"thumbs-up": "👍", "thumbs-down": "👎", "information": "ℹ️", "tick": "✅", "cross": "❌",
	"warning": "⚠️", "plus": "➕", "minus": "➖", "question": "❓", "light-on": "💡", "light-off": "💡",
	"yellow-star": "⭐", "red-star": "⭐", "green-star": "⭐", "blue-star": "⭐",
	"heart": "❤️", "broken-heart": "💔",
}

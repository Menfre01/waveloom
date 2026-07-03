package tool

import "encoding/json"

// ---------------------------------------------------------------------------
// JSON Schema constants for all tools
// ---------------------------------------------------------------------------

var readFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') first to explore directories. Paths without a file extension are likely directories."
    },
    "offset": {
      "type": "integer",
      "description": "Starting line number (0-based, 0 = first line, optional)"
    },
    "limit": {
      "type": "integer",
      "description": "Number of lines to read (optional, default: all)"
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["file_path"]
}`)

var writeFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root). Must be a file, not a directory — use shell('ls') to explore directories first."
    },
    "content": {
      "type": "string",
      "description": "File content to write"
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["file_path", "content"]
}`)

var editFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "File path (absolute, or relative to working_dir / workspace root)"
    },
    "old_string": {
      "type": "string",
      "description": "Text to replace — should match the file content as closely as possible. Minor differences in whitespace, blank lines, and Unicode punctuation (tabs/spaces, smart quotes, em dashes → ASCII, etc.) are auto-corrected when the match is unambiguous. If ambiguous, include more surrounding context lines."
    },
    "new_string": {
      "type": "string",
      "description": "Replacement text. Use empty string to delete the matched text."
    },
    "replace_all": {
      "type": "boolean",
      "description": "Replace all occurrences (default: false, first match only)",
      "default": false
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    }
  },
  "required": ["file_path", "old_string", "new_string"]
}`)

var shellSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "Shell command to execute. Unix/macOS uses bash -c (sh fallback), Windows uses cmd /c. Windows does not support ; for multi-command, use &&."
    },
    "working_dir": {
      "type": "string",
      "description": "Working directory (optional)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (default: 120000, max: 600000)"
    }
  },
  "required": ["command"]
}`)

var webFetchSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {
      "type": "string",
      "description": "URL to fetch (http/https only)"
    },
    "max_size": {
      "type": "integer",
      "description": "Maximum response size in bytes (optional, default: 1MB, max: 5MB)"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "Timeout in milliseconds (optional, default: 30000, max: 120000)"
    }
  },
  "required": ["url"]
}`)

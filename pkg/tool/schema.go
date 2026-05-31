package tool

import "encoding/json"

// ---------------------------------------------------------------------------
// 所有工具的 JSON Schema 常量定义
// ---------------------------------------------------------------------------

var readFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "文件绝对路径"
    },
    "offset": {
      "type": "integer",
      "description": "起始行号（0-based，0 为第一行，可选）"
    },
    "limit": {
      "type": "integer",
      "description": "读取行数（可选，默认全部）"
    },
    "working_dir": {
      "type": "string",
      "description": "工作目录（可选，默认项目根目录）。file_path 为相对路径时基于此目录解析"
    }
  },
  "required": ["file_path"]
}`)

var writeFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "文件绝对路径"
    },
    "content": {
      "type": "string",
      "description": "要写入的文件内容"
    },
    "working_dir": {
      "type": "string",
      "description": "工作目录（可选，默认项目根目录）。file_path 为相对路径时基于此目录解析"
    }
  },
  "required": ["file_path", "content"]
}`)

var editFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "file_path": {
      "type": "string",
      "description": "文件绝对路径"
    },
    "old_string": {
      "type": "string",
      "description": "要替换的文本（必须精确匹配原始内容，含缩进）"
    },
    "new_string": {
      "type": "string",
      "description": "替换后的文本"
    },
    "replace_all": {
      "type": "boolean",
      "description": "是否替换所有匹配项（默认 false，只替换第一个）",
      "default": false
    },
    "working_dir": {
      "type": "string",
      "description": "工作目录（可选，默认项目根目录）。file_path 为相对路径时基于此目录解析"
    }
  },
  "required": ["file_path", "old_string", "new_string"]
}`)

var shellSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "要执行的 Shell 命令"
    },
    "working_dir": {
      "type": "string",
      "description": "工作目录（可选，默认项目根目录）"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "超时时间（毫秒，默认 120000，最大 600000）"
    }
  },
  "required": ["command"]
}`)

var searchFileSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string",
      "description": "Glob 模式（如 **/*.go, *.md, src/**/*_test.go）"
    },
    "working_dir": {
      "type": "string",
      "description": "搜索起始目录（可选，默认项目根目录）"
    }
  },
  "required": ["pattern"]
}`)

var grepSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "pattern": {
      "type": "string",
      "description": "正则表达式（RE2 语法）"
    },
    "include": {
      "type": "string",
      "description": "Glob 模式过滤文件（可选，如 *.go）"
    },
    "working_dir": {
      "type": "string",
      "description": "搜索起始目录（可选）"
    },
    "case_insensitive": {
      "type": "boolean",
      "description": "忽略大小写（默认 false）",
      "default": false
    },
    "context_lines": {
      "type": "integer",
      "description": "匹配行上下文的行数（可选，default 0）"
    }
  },
  "required": ["pattern"]
}`)

var lsSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "path": {
      "type": "string",
      "description": "目录路径（可选，默认项目根目录）"
    },
    "depth": {
      "type": "integer",
      "description": "递归深度（可选，默认 1）",
      "default": 1
    },
    "working_dir": {
      "type": "string",
      "description": "工作目录（可选，默认项目根目录）。path 为相对路径时基于此目录解析"
    }
  },
  "required": []
}`)

var webFetchSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "url": {
      "type": "string",
      "description": "要获取的 URL（仅支持 http/https）"
    },
    "max_size": {
      "type": "integer",
      "description": "最大响应字节数（可选，默认 1MB，最大 5MB）"
    },
    "timeout_ms": {
      "type": "integer",
      "description": "超时时间（毫秒，可选，默认 30000，最大 120000）"
    }
  },
  "required": ["url"]
}`)

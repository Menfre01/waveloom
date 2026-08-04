package acp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

// ErrLineTooLong 表示单条消息超过传输上限(maxLineLen)。
// 可恢复:超长行的剩余字节会被丢弃,Server 回 parse error 后可从下一行
// 继续读取(恶意客户端 DoS 防护,不关闭整个连接)。
// 注意:不能使用 bufio.Scanner——其 ErrTooLong 是终态,后续 Scan 永远失败。
var ErrLineTooLong = errors.New("acp transport: message line too long")

// maxLineLen 单条 JSON-RPC 消息的最大长度(10MB)。
const maxLineLen = 10 * 1024 * 1024

// ---------------------------------------------------------------------------
// StdioTransport — 基于 stdin/stdout 的行分隔 JSON-RPC 传输
// ---------------------------------------------------------------------------

// StdioTransport 通过标准输入输出实现 ACP stdio 传输。
// 每条 JSON-RPC 消息独占一行(换行符分隔),消息内不含嵌入换行。
type StdioTransport struct {
	reader  *bufio.Reader
	writer  io.Writer
	writeMu sync.Mutex // 保护 stdout 写入原子性
}

// NewStdioTransport 创建基于 os.Stdin / os.Stdout 的传输。
func NewStdioTransport() *StdioTransport {
	return NewStdioTransportIO(os.Stdin, os.Stdout)
}

// NewStdioTransportIO 创建基于指定 reader/writer 的传输(用于测试)。
func NewStdioTransportIO(r io.Reader, w io.Writer) *StdioTransport {
	return &StdioTransport{
		reader: bufio.NewReaderSize(r, 64*1024),
		writer: w,
	}
}

// Send 将 JSON-RPC 消息写入 stdout。
// 自动 JSON 序列化并追加换行符。
func (t *StdioTransport) Send(msg any) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("acp transport marshal: %w", err)
	}

	// 验证:JSON 序列化结果不应包含嵌入换行
	if strings.ContainsAny(string(data), "\n\r") {
		return fmt.Errorf("acp transport: message contains embedded newline")
	}

	if _, err := fmt.Fprintf(t.writer, "%s\n", data); err != nil {
		return fmt.Errorf("acp transport write: %w", err)
	}

	return nil
}

// Receive 从 stdin 读取下一条 JSON-RPC 消息。
// 返回原始 JSON 字节,由调用方解析。
func (t *StdioTransport) Receive() (json.RawMessage, error) {
	// 使用 for 循环跳过空行
	for {
		line, err := t.readLine()
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return json.RawMessage(line), nil
	}
}

// readLine 读取一行。行超长时丢弃该行剩余字节并返回 ErrLineTooLong,
// 输入流位置推进到换行之后,后续行可正常读取。
func (t *StdioTransport) readLine() (string, error) {
	var line []byte
	for {
		frag, err := t.reader.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			line = append(line, frag...)
			if len(line) > maxLineLen {
				// 超长:丢弃该行剩余字节(继续读到换行),可恢复
				for err == bufio.ErrBufferFull {
					_, err = t.reader.ReadSlice('\n')
				}
				if err == io.EOF {
					return "", io.EOF
				}
				if err != nil {
					return "", err
				}
				return "", ErrLineTooLong
			}
			continue
		}
		if err == io.EOF {
			if len(line)+len(frag) == 0 {
				return "", io.EOF
			}
			// 最后一行无换行结尾:返回已有内容
			line = append(line, frag...)
			if len(line) > maxLineLen {
				return "", ErrLineTooLong
			}
			return string(line), nil
		}
		if err != nil {
			return "", err
		}
		line = append(line, frag...)
		if len(line) > maxLineLen {
			return "", ErrLineTooLong
		}
		return string(line), nil
	}
}

// Close 关闭传输。stdio transport 无需显式关闭,仅用于接口兼容。
func (t *StdioTransport) Close() error {
	return nil
}

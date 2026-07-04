// Package environment — Waveloom 启动时探测宿主编译/运行时工具链可用性。
//
// 本文件实现自更新流程：下载 GitHub Release tar.gz → 解压 → 替换当前二进制。
// TUI 通过 Progress 回调获取进度并渲染到 tool 段落。
package environment

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SelfUpdatePhase 表示自更新流程的阶段。
type SelfUpdatePhase string

const (
	PhaseDownload SelfUpdatePhase = "download"
	PhaseExtract  SelfUpdatePhase = "extract"
	PhaseInstall  SelfUpdatePhase = "install"
	PhaseDone     SelfUpdatePhase = "done"
)

// SelfUpdateProgress 是自更新进度回调。
// phase 是当前阶段；pct 是 0-100 的百分比（仅 download 阶段有意义）；detail 是人类可读的描述。
type SelfUpdateProgress func(phase SelfUpdatePhase, pct int, detail string)

// BuildDownloadURL 返回当前平台对应的 GitHub Release 下载地址。
// Windows 使用 .zip，其他平台使用 .tar.gz。
func BuildDownloadURL() string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf(
		"https://github.com/Menfre01/waveloom/releases/latest/download/waveloom_%s_%s.%s",
		runtime.GOOS, runtime.GOARCH, ext,
	)
}

// SelfUpdate 下载 release tar.gz 到临时目录，解压并替换 currentPath 指向的二进制文件。
// 成功时新二进制已替换 currentPath；失败时尝试回滚（从 .old 备份恢复）。
// progress 可为 nil，此时不报告进度。
func SelfUpdate(ctx context.Context, currentPath, downloadURL string, progress SelfUpdateProgress) error {
	report := func(phase SelfUpdatePhase, pct int, detail string) {
		if progress != nil {
			progress(phase, pct, detail)
		}
	}

	// Phase 1: 下载
	report(PhaseDownload, 0, fmt.Sprintf("Downloading %s ...", filepath.Base(downloadURL)))

	tmpDir, err := os.MkdirTemp("", "waveloom-update-")
	if err != nil {
		return fmt.Errorf("创建临时目录: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	isZip := strings.HasSuffix(downloadURL, ".zip")
	archiveExt := ".tar.gz"
	if isZip {
		archiveExt = ".zip"
	}
	archivePath := filepath.Join(tmpDir, "waveloom"+archiveExt)
	if err := downloadWithProgress(ctx, downloadURL, archivePath, func(downloaded, total int64, pct int) {
		mbDown := float64(downloaded) / (1024 * 1024)
		mbTotal := float64(total) / (1024 * 1024)
		report(PhaseDownload, pct, fmt.Sprintf("  %.1f MB / %.1f MB (%d%%)", mbDown, mbTotal, pct))
	}); err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}

	// Phase 2: 解压
	report(PhaseExtract, 100, "Extracting ...")

	var newBinary string
	if isZip {
		newBinary, err = extractWaveloomZip(archivePath, tmpDir)
	} else {
		newBinary, err = extractWaveloom(archivePath, tmpDir)
	}
	if err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	// Phase 3: 安装
	report(PhaseInstall, 100, fmt.Sprintf("Installing to %s ...", currentPath))

	backupPath := currentPath + ".old"
	if err := os.Rename(currentPath, backupPath); err != nil {
		return fmt.Errorf("备份旧版本失败: %w", err)
	}

	if err := copyFile(newBinary, currentPath); err != nil {
		// 回滚
		_ = os.Rename(backupPath, currentPath)
		return fmt.Errorf("安装失败: %w", err)
	}

	_ = os.Remove(backupPath)

	if err := os.Chmod(currentPath, 0o755); err != nil {
		return fmt.Errorf("设置权限失败: %w", err)
	}

	report(PhaseDone, 100, "installed")
	return nil
}

// ---------------------------------------------------------------------------
// 内部实现
// ---------------------------------------------------------------------------

// downloadWithProgress 下载文件，通过回调报告进度。
func downloadWithProgress(ctx context.Context, url, dst string, onProgress func(downloaded, total int64, pct int)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "waveloom")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	total := resp.ContentLength
	pr := &progressReader{
		r:     resp.Body,
		total: total,
		onProgress: func(downloaded int64) {
			pct := 0
			if total > 0 {
				pct = int(downloaded * 100 / total)
			}
			onProgress(downloaded, total, pct)
		},
	}

	_, err = io.Copy(f, pr)
	return err
}

// progressReader 包装 io.Reader，每读取数据后调用一次回调。
type progressReader struct {
	r          io.Reader
	total      int64
	downloaded int64
	onProgress func(downloaded int64)
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.downloaded += int64(n)
	if n > 0 && pr.onProgress != nil {
		pr.onProgress(pr.downloaded)
	}
	return n, err
}

// extractWaveloom 从 tar.gz 中提取名为 "waveloom" 的二进制到临时目录，返回路径。
func extractWaveloom(tarballPath, tmpDir string) (string, error) {
	f, err := os.Open(tarballPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	gzReader, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer func() { _ = gzReader.Close() }()

	tarReader := tar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		if header.Name == "waveloom" && header.Typeflag == tar.TypeReg {
			outPath := filepath.Join(tmpDir, "waveloom")
			out, err := os.Create(outPath)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tarReader); err != nil {
				_ = out.Close()
				return "", err
			}
			_ = out.Close()
			if err := os.Chmod(outPath, 0o755); err != nil {
				return "", err
			}
			return outPath, nil
		}
	}

	return "", fmt.Errorf("waveloom binary not found in tarball")
}

// extractWaveloomZip 从 .zip 中提取 waveloom 二进制（Windows 上为 waveloom.exe）。
func extractWaveloomZip(zipPath, tmpDir string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()

	binName := "waveloom"
	if runtime.GOOS == "windows" {
		binName = "waveloom.exe"
	}

	for _, f := range r.File {
		if f.Name == binName {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer func() { _ = rc.Close() }()

			outPath := filepath.Join(tmpDir, binName)
			out, err := os.Create(outPath)
			if err != nil {
				return "", err
			}
			defer func() { _ = out.Close() }()

			if _, err := io.Copy(out, rc); err != nil {
				return "", err
			}
			return outPath, nil
		}
	}

	return "", fmt.Errorf("waveloom binary not found in zip")
}

// copyFile 复制文件内容及权限。
func copyFile(src, dst string) error {
	srcF, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcF.Close() }()

	dstF, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = dstF.Close() }()

	_, err = io.Copy(dstF, srcF)
	return err
}

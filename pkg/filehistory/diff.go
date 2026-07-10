package filehistory

import "strings"

// DiffStats holds the diff summary for a snapshot.
type DiffStats struct {
	FilesChanged int
	Files        []string
}

// ComputeDiffStats computes diff statistics by comparing two snapshots.
func ComputeDiffStats(prev, curr FileHistorySnapshot) DiffStats {
	ds := DiffStats{}
	seen := make(map[string]bool)

	for filePath, currBackup := range curr.TrackedFileBackups {
		seen[filePath] = true
		prevBackup, existed := prev.TrackedFileBackups[filePath]
		if !existed {
			// New file
			ds.FilesChanged++
			ds.Files = append(ds.Files, filePath)
			continue
		}
		if prevBackup.BackupFileName != currBackup.BackupFileName || prevBackup.Version != currBackup.Version {
			ds.FilesChanged++
			ds.Files = append(ds.Files, filePath)
		}
	}

	// Files in prev but not in curr → deleted
	for filePath := range prev.TrackedFileBackups {
		if !seen[filePath] {
			ds.FilesChanged++
			ds.Files = append(ds.Files, filePath)
		}
	}

	return ds
}

// FileListDisplay returns a human-readable file list for display.
func FileListDisplay(files []string) string {
	if len(files) == 0 {
		return ""
	}
	if len(files) <= 3 {
		return strings.Join(files, ", ")
	}
	return strings.Join(files[:3], ", ") + " and " + itoa(len(files)-3) + " more"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

## Write File

Use `write` to create new files or completely overwrite existing ones.

### Parameters

- `file_path` — required. Path to write.
- `content` — required. Full file content.

### Tips

- Use for new files or when rewriting is simpler than multiple edits
- Prefer `edit` with hunk format for targeted changes
- File state is tracked internally for subsequent edits

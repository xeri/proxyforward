# PostToolUse hook (Edit|Write): new Go code must be gofmt-clean (root CLAUDE.md
# "Enforcement"). Exit 2 feeds the message back to the model for an immediate fix.
try { $in = [Console]::In.ReadToEnd() | ConvertFrom-Json } catch { exit 0 }
$path = $in.tool_input.file_path
if (-not $path -or $path -notmatch '\.go$') { exit 0 }
if (-not (Get-Command gofmt -ErrorAction SilentlyContinue)) { exit 0 }
$out = & gofmt -l $path
if ($out) {
    [Console]::Error.WriteLine("gofmt: $path is not gofmt-clean. Run 'gofmt -w $path'.")
    exit 2
}
exit 0

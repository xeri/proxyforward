# PreToolUse hook: deny git commit commands whose message embeds a claude.ai session URL.
# Input: hook JSON on stdin. Output: permissionDecision deny when both patterns match.
$raw = [Console]::In.ReadToEnd()
if ([string]::IsNullOrWhiteSpace($raw)) { exit 0 }
try { $payload = $raw | ConvertFrom-Json } catch { exit 0 }
$cmd = $payload.tool_input.command
if ([string]::IsNullOrEmpty($cmd)) { exit 0 }
if ($cmd -match 'git\s+commit' -and $cmd -match 'claude\.ai/code/session') {
    $reason = 'Commit blocked: the message embeds a claude.ai session URL (Claude-Session trailer). Session IDs must not enter git history. Remove the trailer and commit again.'
    $out = @{
        hookSpecificOutput = @{
            hookEventName            = 'PreToolUse'
            permissionDecision       = 'deny'
            permissionDecisionReason = $reason
        }
    } | ConvertTo-Json -Compress -Depth 3
    [Console]::Out.WriteLine($out)
    exit 0
}
exit 0

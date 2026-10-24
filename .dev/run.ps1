# Runs webframe.exe in the interactive desktop session via a scheduled task,
# since processes started over ssh have no desktop. Inputs staged next to this
# script by .dev/vm: args.txt (one arg per line), env.txt (NAME=value lines)
# and stdin.txt. Prints the task's stdout/stderr and exits with its code.
$ErrorActionPreference = "Stop"
$dir = $PSScriptRoot
$timeout = if ($env:VM_TIMEOUT) { [int]$env:VM_TIMEOUT } else { 120 }

$task = @'
$ErrorActionPreference = "Stop"
$dir = $PSScriptRoot
function Quote([string]$a) {
  if ($a -ne "" -and $a -notmatch '[\s"]') { return $a }
  return '"' + ($a -replace '(\\*)"', '$1$1\"' -replace '(\\+)$', '$1$1') + '"'
}
foreach ($kv in Get-Content "$dir\env.txt") {
  $k, $v = $kv -split "=", 2
  if ($k) { Set-Item "env:$k" $v }
}
$opts = @{}
$argv = @(Get-Content "$dir\args.txt")
if ($argv.Count) { $opts.ArgumentList = ($argv | ForEach-Object { Quote $_ }) -join " " }
$p = Start-Process -FilePath "$dir\webframe.exe" -WorkingDirectory $dir -NoNewWindow -PassThru @opts `
  -RedirectStandardInput "$dir\stdin.txt" -RedirectStandardOutput "$dir\out.txt" -RedirectStandardError "$dir\err.txt"
$null = $p.Handle # without this, ExitCode is empty after the process exits
$p.WaitForExit()
Set-Content "$dir\code.txt" $p.ExitCode
'@
Set-Content "$dir\task.ps1" $task
Remove-Item "$dir\out.txt", "$dir\err.txt", "$dir\code.txt" -ErrorAction SilentlyContinue

$name = "webframe-run"
$action = New-ScheduledTaskAction -Execute "powershell.exe" `
  -Argument "-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File `"$dir\task.ps1`""
$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive
Register-ScheduledTask -TaskName $name -Action $action -Principal $principal -Force | Out-Null
Start-ScheduledTask -TaskName $name

$deadline = (Get-Date).AddSeconds($timeout)
while (-not (Test-Path "$dir\code.txt")) {
  if ((Get-Date) -gt $deadline) {
    Stop-ScheduledTask -TaskName $name
    Get-Process webframe -ErrorAction SilentlyContinue | Stop-Process -Force
    Start-Sleep -Milliseconds 500
    break
  }
  Start-Sleep -Milliseconds 200
}
Unregister-ScheduledTask -TaskName $name -Confirm:$false

foreach ($f in "out", "err") {
  if (Test-Path "$dir\$f.txt") {
    $s = [IO.File]::ReadAllText("$dir\$f.txt")
    if ($f -eq "out") { [Console]::Out.Write($s) } else { [Console]::Error.Write($s) }
  }
}
if (-not (Test-Path "$dir\code.txt")) { exit 124 }
exit [int](Get-Content "$dir\code.txt")

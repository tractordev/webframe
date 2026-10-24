# Prepares a Windows guest for .dev/vm: enables auto-logon (Lima's unattended
# install only logs on once), since webframe runs in the desktop session.
# Prints "session" if a desktop session is active.
$ErrorActionPreference = "Stop"
$k = "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon"
if ((Get-ItemProperty $k).AutoAdminLogon -ne "1") {
  $pw = (Get-Content "$env:USERPROFILE\password.txt" -Raw).Trim()
  Set-ItemProperty $k AutoAdminLogon "1"
  Set-ItemProperty $k DefaultUserName $env:USERNAME
  Set-ItemProperty $k DefaultPassword $pw
  Remove-ItemProperty $k AutoLogonCount -ErrorAction SilentlyContinue
  [Console]::Error.WriteLine("auto-logon enabled")
}
$desktop = Get-CimInstance Win32_Process -Filter "Name='explorer.exe'" |
  Where-Object { (Invoke-CimMethod -InputObject $_ -MethodName GetOwner).User -eq $env:USERNAME }
if ($desktop) { "session" }

# Signs Windows files with Authenticode when the release has a code signing
# certificate; without one it changes nothing (an unsigned release: SmartScreen
# warns until the files build reputation, see RELEASING.md).
#
#   pwsh build/windows/sign.ps1 bin/sitemoment-desktop.exe [more files]
#
# Environment (the release workflow fills them from secrets):
#   WINDOWS_CERT_PFX        path to the .pfx (the workflow decodes WINDOWS_CERT_PFX_BASE64 into it)
#   WINDOWS_CERT_PASSWORD   its password: read into a SecureString and handed to
#                           Import-PfxCertificate in-process, never passed as a
#                           command-line argument to signtool.exe (or any other
#                           external process), where it would sit in that
#                           process's argv for anything else on the runner to read
#   WINDOWS_TIMESTAMP_URL   RFC 3161 timestamp server (default http://timestamp.digicert.com)
param([Parameter(Mandatory = $true, ValueFromRemainingArguments = $true)][string[]]$Files)
$ErrorActionPreference = 'Stop'
if (-not $env:WINDOWS_CERT_PFX) {
  Write-Host "sign.ps1: WINDOWS_CERT_PFX is not set; $($Files -join ', ') stay unsigned (unsigned release)"
  exit 0
}
$signtool = (Get-Command signtool.exe -ErrorAction SilentlyContinue).Source
if (-not $signtool) {
  $signtool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
    Sort-Object FullName -Descending | Select-Object -First 1 -ExpandProperty FullName
}
if (-not $signtool) { throw 'sign.ps1: signtool.exe not found (install the Windows SDK)' }

$securePassword = ConvertTo-SecureString -String $env:WINDOWS_CERT_PASSWORD -AsPlainText -Force
$cert = Import-PfxCertificate -FilePath $env:WINDOWS_CERT_PFX -CertStoreLocation Cert:\CurrentUser\My -Password $securePassword
$thumbprint = $cert.Thumbprint

$timestamp = if ($env:WINDOWS_TIMESTAMP_URL) { $env:WINDOWS_TIMESTAMP_URL } else { 'http://timestamp.digicert.com' }
foreach ($file in $Files) {
  & $signtool sign /sha1 $thumbprint /fd SHA256 /tr $timestamp /td SHA256 /d 'SiteMoment' /du 'https://sitemoment.com/seo/' $file
  if ($LASTEXITCODE -ne 0) { throw "sign.ps1: signtool sign failed for $file" }
  & $signtool verify /pa /tw $file
  if ($LASTEXITCODE -ne 0) { throw "sign.ps1: the signature of $file does not verify" }
}

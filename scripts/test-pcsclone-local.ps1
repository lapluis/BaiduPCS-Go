param([string]$Binary = (Join-Path $PSScriptRoot '..\build\pcsclone.exe'))
$ErrorActionPreference = 'Stop'
$Binary = [IO.Path]::GetFullPath($Binary)
if (!(Test-Path -LiteralPath $Binary)) { throw "Binary not found: $Binary" }

$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('pcsclone-local-' + [guid]::NewGuid().ToString('N'))
$config = Join-Path $testRoot 'config\pcs_config.json'
New-Item -ItemType Directory -Path $testRoot -Force | Out-Null

function Invoke-Pcs([string[]]$Arguments) {
  $oldPreference = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    $result = & $Binary '--config' $config @Arguments 2>$null
    $exitCode = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $oldPreference
  }
  if ($exitCode -ne 0) { throw ('Command failed (' + $exitCode + '): ' + ($Arguments -join ' ')) }
  return $result
}

function Expect-Code([int]$Code, [string[]]$Arguments) {
  $oldPreference = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    & $Binary '--config' $config @Arguments 2>$null | Out-Null
    $exitCode = $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $oldPreference
  }
  if ($exitCode -ne $Code) {
    throw ('Expected exit ' + $Code + ', got ' + $exitCode + ': ' + ($Arguments -join ' '))
  }
}

try {
  $version = & $Binary --version
  if ($LASTEXITCODE -ne 0 -or $version -notmatch '^pcsclone ') { throw 'Version command failed' }
  if ((& $Binary help | Out-String) -notmatch 'Core file operations') { throw 'Global help missing commands' }
  if ((& $Binary help baidu share create | Out-String) -notmatch 'pcsclone baidu share create') { throw 'Nested help failed' }
  if (((& $Binary completion bash) | Out-String) -notmatch 'baidu share') { throw 'bash completion missing nested commands' }
  Expect-Code 2 @('completion', 'fish')

  $source = Join-Path $testRoot 'source'
  $target = Join-Path $testRoot 'target'
  Invoke-Pcs @('mkdir', (Join-Path $source 'nested')) | Out-Null
  Invoke-Pcs @('mkdir', (Join-Path $source 'empty')) | Out-Null
  [IO.File]::WriteAllText((Join-Path $source 'nested\hello.txt'), 'hello' + "`n")
  [IO.File]::WriteAllText((Join-Path $source 'zero.txt'), '')
  [IO.File]::WriteAllText((Join-Path $source 'skip.tmp'), 'skip')

  Invoke-Pcs @('copy', $source, $target, '--exclude', '*.tmp', '--create-empty-src-dirs', '--dry-run') | Out-Null
  if (Test-Path -LiteralPath $target) { throw 'Dry-run created destination' }
  Invoke-Pcs @('copy', $source, $target, '--exclude', '*.tmp', '--create-empty-src-dirs', '--checksum') | Out-Null
  if (!(Test-Path -LiteralPath (Join-Path $target 'empty'))) { throw 'Empty source directory missing' }
  if (Test-Path -LiteralPath (Join-Path $target 'skip.tmp')) { throw 'Excluded file copied' }

  if (((Invoke-Pcs @('ls', $target)) | Out-String) -notmatch 'nested/hello.txt') { throw 'ls is not recursive by default' }
  if (((Invoke-Pcs @('ls', $target, '--recursive=false')) | Out-String) -match 'nested/hello.txt') { throw 'ls --recursive=false recursed' }
  if (((Invoke-Pcs @('lsf', $target)) | Out-String) -match 'nested/hello.txt') { throw 'lsf recursed by default' }
  if (((Invoke-Pcs @('lsf', $target, '-R')) | Out-String) -notmatch 'nested/hello.txt') { throw 'lsf -R did not recurse' }

  $entries = Invoke-Pcs @('lsjson', $target) | ConvertFrom-Json
  if (@($entries).Count -ne 3) { throw 'lsjson entry count mismatch' }
  $size = Invoke-Pcs @('size', $target, '--json') | ConvertFrom-Json
  if ($size.count -ne 2 -or $size.bytes -ne 6) { throw 'size mismatch' }
  $stat = Invoke-Pcs @('stat', (Join-Path $target 'nested\hello.txt'), '--hash') | ConvertFrom-Json
  if ($stat.Hash -ne 'b1946ac92492d2347c6235b4d2611184' -or $stat.Remote -ne $false) { throw 'stat mismatch' }

  Invoke-Pcs @('check', $source, $target, '--exclude', '*.tmp') | Out-Null
  [IO.File]::WriteAllText((Join-Path $target 'extra.txt'), 'extra')
  Expect-Code 1 @('check', $source, $target, '--exclude', '*.tmp')
  Expect-Code 1 @('sync', $source, $target, '--exclude', '*.tmp', '--max-delete', '0')
  Invoke-Pcs @('sync', $source, $target, '--exclude', '*.tmp', '--max-delete', '1') | Out-Null
  if (Test-Path -LiteralPath (Join-Path $target 'extra.txt')) { throw 'sync did not delete extra file' }

  Invoke-Pcs @('moveto', (Join-Path $target 'nested\hello.txt'), (Join-Path $target 'renamed.txt')) | Out-Null
  Invoke-Pcs @('deletefile', (Join-Path $target 'renamed.txt')) | Out-Null
  Invoke-Pcs @('delete', $target, '--include', '*.txt') | Out-Null
  Invoke-Pcs @('rmdirs', $target) | Out-Null
  Invoke-Pcs @('rmdir', $target) | Out-Null

  $moved = Join-Path $testRoot 'moved'
  Invoke-Pcs @('move', $source, $moved, '--delete-empty-src-dirs') | Out-Null
  if (!(Test-Path -LiteralPath (Join-Path $moved 'nested\hello.txt'))) { throw 'move result missing' }
  Invoke-Pcs @('purge', $moved) | Out-Null

  Expect-Code 2 @('copy', $source, $source, '--transfers', '0')
  Expect-Code 2 @('purge', $source, '--exclude', '*.txt')
  Expect-Code 2 @('not-a-command')
  Expect-Code 1 @('copy', $source, $source)

  $legacyDir = Join-Path $testRoot 'legacy'
  New-Item -ItemType Directory -Path $legacyDir -Force | Out-Null
  $legacy = @'
{
  "baidu_active_uid": 111,
  "baidu_user_list": [
    {"uid": 111, "name": "alice", "bduss": "SECRET-BDUSS-111", "stoken": "SECRET-STOKEN", "cookies": "", "workdir": "/old"},
    {"uid": 222, "name": "second", "bduss": "SECRET-BDUSS-222", "cookies": "", "workdir": "/"}
  ]
}
'@
  [IO.File]::WriteAllText((Join-Path $legacyDir 'pcs_config.json'), $legacy, (New-Object System.Text.UTF8Encoding($false)))
  [IO.File]::WriteAllText((Join-Path $legacyDir 'rc_remotes.json'), '{"bd":111,"cjk":222}', (New-Object System.Text.UTF8Encoding($false)))
  $legacyBefore = [IO.File]::ReadAllText((Join-Path $legacyDir 'pcs_config.json'))

  $importOut = Invoke-Pcs @('config', 'import-pcsgo', '--from', $legacyDir) | Out-String
  if ($importOut -notmatch 'Remotes: bd, cjk') { throw 'Import output missing remotes' }
  $importJson = Invoke-Pcs @('config', 'import-pcsgo', '--from', $legacyDir, '--json') | ConvertFrom-Json
  if (@($importJson.remotes).Count -ne 2 -or $importJson.remotes[0] -ne 'bd' -or $importJson.remotes[1] -ne 'cjk') {
    throw 'Import JSON remotes mismatch'
  }
  $remotes = Invoke-Pcs @('listremotes') | Out-String
  if ($remotes -notmatch 'bd:' -or $remotes -notmatch 'cjk:') { throw 'Imported aliases missing' }
  $show = Invoke-Pcs @('config', 'show', '--json') | Out-String
  if ($show -match 'SECRET') { throw 'config show leaked credentials' }
  $showJson = $show | ConvertFrom-Json
  if (@($showJson).Count -ne 2) { throw 'config show count mismatch' }
  $files = Invoke-Pcs @('config', 'file') | Out-String
  if ($files -notmatch [regex]::Escape($config) -or $files -notmatch 'rc_remotes.json') { throw 'config file output mismatch' }
  if ([IO.File]::ReadAllText((Join-Path $legacyDir 'pcs_config.json')) -ne $legacyBefore) { throw 'Legacy config was modified' }
  if (((Invoke-Pcs @('config', 'show', 'bd')) | Out-String) -match 'SECRET') { throw 'config show leaked credentials for one remote' }
  Invoke-Pcs @('config', 'rename', 'bd', 'backup') | Out-Null
  Invoke-Pcs @('config', 'delete', 'backup', '--keep-account') | Out-Null
  Expect-Code 1 @('baidu', 'link', 'bd:/file')
  Expect-Code 2 @('baidu', 'link', 'local-file')

  'PASS: pcsclone local operations, help, exit codes, configuration import and redaction'
} finally {
  $resolved = [IO.Path]::GetFullPath($testRoot)
  $expectedParent = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
  if (!$resolved.StartsWith($expectedParent, [StringComparison]::OrdinalIgnoreCase) -or (Split-Path $resolved -Leaf) -notmatch '^pcsclone-local-[a-f0-9]{32}$') {
    throw 'Unsafe cleanup path'
  }
  Remove-Item -LiteralPath $resolved -Recurse -Force
}

exit 0

param([string]$Binary = (Join-Path $PSScriptRoot '..\build\BaiduPCS-Go.exe'))
$ErrorActionPreference = 'Stop'
$Binary = [IO.Path]::GetFullPath($Binary)
$testRoot = Join-Path ([IO.Path]::GetTempPath()) ('baidupcs-rc-local-' + [guid]::NewGuid().ToString('N'))
$priorConfig = $env:BAIDUPCS_GO_CONFIG_DIR
New-Item -ItemType Directory -Path $testRoot -Force | Out-Null
$env:BAIDUPCS_GO_CONFIG_DIR = Join-Path $testRoot 'config'
function Invoke-Rc([string[]]$Arguments) {
 $result = & $Binary @Arguments
 if ($LASTEXITCODE -ne 0) { throw ('Command failed: ' + ($Arguments -join ' ')) }
 return $result
}
function Expect-Failure([string[]]$Arguments) {
 & $Binary @Arguments 2>$null | Out-Null
 if ($LASTEXITCODE -eq 0) { throw ('Expected failure: ' + ($Arguments -join ' ')) }
}
try {
 $source = Join-Path $testRoot 'source'
 $target = Join-Path $testRoot 'target'
 Invoke-Rc @('rc','mkdir',(Join-Path $source 'nested')) | Out-Null
 Invoke-Rc @('rc','mkdir',(Join-Path $source 'empty')) | Out-Null
 [IO.File]::WriteAllText((Join-Path $source 'nested\hello.txt'),'local CLI fixture')
 [IO.File]::WriteAllText((Join-Path $source 'zero.txt'),'')
 [IO.File]::WriteAllText((Join-Path $source 'skip.tmp'),'excluded')
 Invoke-Rc @('copy',$source,$target,'--exclude','*.tmp','--create-empty-src-dirs','--dry-run') | Out-Null
 if (Test-Path -LiteralPath $target) { throw 'Dry-run created destination' }
 $logPath = Join-Path $testRoot 'events.jsonl'
 Invoke-Rc @('copy',$source,$target,'--exclude','*.tmp','--create-empty-src-dirs','--checksum','--use-json-log','--log-file',$logPath,'-P') | Out-Null
 foreach ($line in Get-Content -LiteralPath $logPath) { $null = $line | ConvertFrom-Json }
 $entries = (Invoke-Rc @('lsjson',$target,'--recursive')) | ConvertFrom-Json
 if (@($entries | Where-Object { $_.Path -eq 'nested/hello.txt' }).Count -ne 1) { throw 'JSON listing mismatch' }
 if (Test-Path -LiteralPath (Join-Path $target 'skip.tmp')) { throw 'Excluded file copied' }
 Invoke-Rc @('check',$source,$target,'--exclude','*.tmp') | Out-Null
 Invoke-Rc @('lsf',$target,'--recursive','--files-only') | Out-Null
 Invoke-Rc @('lsd',$target) | Out-Null
 Invoke-Rc @('rc','tree',$target,'--depth','2') | Out-Null
 $size = (Invoke-Rc @('size',$target)) | ConvertFrom-Json
 if ($size.count -ne 2) { throw 'Size count mismatch' }
 Invoke-Rc @('copyto',(Join-Path $source 'nested\hello.txt'),(Join-Path $target 'extra.txt')) | Out-Null
 Expect-Failure @('sync',$source,$target,'--exclude','*.tmp','--max-delete','0')
 Invoke-Rc @('sync',$source,$target,'--exclude','*.tmp','--max-delete','1') | Out-Null
 if (Test-Path -LiteralPath (Join-Path $target 'extra.txt')) { throw 'Sync did not delete extra file' }
 Invoke-Rc @('moveto',(Join-Path $target 'nested\hello.txt'),(Join-Path $target 'renamed.txt')) | Out-Null
 if (Test-Path -LiteralPath (Join-Path $target 'nested\hello.txt')) { throw 'Moveto retained source' }
 Invoke-Rc @('deletefile',(Join-Path $target 'renamed.txt')) | Out-Null
 Invoke-Rc @('delete',$target,'--include','*.txt') | Out-Null
 Invoke-Rc @('rmdirs',$target) | Out-Null
 Invoke-Rc @('rmdir',$target) | Out-Null
 $moveTarget = Join-Path $testRoot 'moved'
 Invoke-Rc @('move',$source,$moveTarget,'--delete-empty-src-dirs') | Out-Null
 Invoke-Rc @('purge',$moveTarget) | Out-Null
 Invoke-Rc @('listremotes') | Out-Null
 Expect-Failure @('copy',$source,$source)
 Expect-Failure @('purge',$source,'--exclude','*.txt')
 Expect-Failure @('copy',$source,$target,'--transfers','0')
 'PASS: local CLI commands, filters, JSON logs, dry-run, deletion limits and exits'
} finally {
 $env:BAIDUPCS_GO_CONFIG_DIR = $priorConfig
 $resolved = [IO.Path]::GetFullPath($testRoot)
 $expectedParent = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\') + '\'
 if (!$resolved.StartsWith($expectedParent,[StringComparison]::OrdinalIgnoreCase) -or (Split-Path $resolved -Leaf) -notmatch '^baidupcs-rc-local-[a-f0-9]{32}$') { throw 'Unsafe cleanup path' }
 Remove-Item -LiteralPath $resolved -Recurse -Force
}

exit 0

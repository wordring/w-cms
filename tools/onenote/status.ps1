# ─────────────────────────────────────────────────────────────────────────
# ワンノートの吸い出しの様子（2026-09-30）——いま動いているか・前の回・次の回を見て、手で始める・止める。
#
# 利用者:「吸出しはパソコンを使っていない時間帯でお願いします」「こちらで明示的に始めたり止めたりでも良いです」
# 「吸出しが動いているかどうかわかる方法も用意してください」。
#
#   - 動いているかは、吸い出しの印（名前つきの Mutex `w-cms-onenote-extract`・extract.ps1）で見る——タスクから
#     始めた回も、手で流した回も分かる。
#   - タスク「w-cms ワンノートの吸い出し」の状態（前の回の結果・次の回）と、記録の最後の数行も出す。
#     タスクは**予定を持たない入れ物**で、始めるのも止めるのも人（2026-09-30 利用者:「時間当たりの吸出しが多すぎる
#     ように思います」「手動で開始停止するので、使っていないときに動かすような細工は必要なくなりました」）。
#   - [S] で始める——タスクの条件を**無視して**すぐ始める（`RunEx` の TASK_RUN_IGNORE_CONSTRAINTS）。いまのタスクに
#     条件は無いが、「10分操作が無いときだけ」の条件を付けていた日に、Start-ScheduledTask やタスク スケジューラの
#     「実行する」では「待っています」のまま始まらなかった（2026-09-30 に試しのタスクで確かめた）——条件を戻しても効くように。
#   - [T] で止める——途中で止めても、目録は1ページごとに保存しているので次の回が続きから取る。
#
# 動かし方（窓で見る・5秒ごとに新しくする）:
#     powershell.exe -NoProfile -ExecutionPolicy Bypass -File status.ps1 [-Notebook 板金部]
#   -Once は様子を1回だけ出して終わる。-Start・-Stop は窓を出さずに始める・止める（ほかの道具から使うとき）。
# ⚠ このファイルは BOM 付き UTF-8 で保存すること（PowerShell 5.1 は BOM 無しを cp932 として読む）。
# ─────────────────────────────────────────────────────────────────────────
param([string]$Notebook = '板金部', [switch]$Once, [switch]$Start, [switch]$Stop)
$ErrorActionPreference = 'Stop'
$utf8 = New-Object System.Text.UTF8Encoding($false)
$taskName = 'w-cms ワンノートの吸い出し'
$root = Join-Path ([Environment]::GetFolderPath('Desktop')) ('w-cms\ワンノート\' + $Notebook)
$logFile = Join-Path $root '吸い出しの記録.log'
$machineFile = Join-Path $root '吸い出している機械.txt'
$trafficFile = Join-Path $env:LOCALAPPDATA 'w-cms\ワンノートの通信量.json'
$script:note = '' # 最後の操作の結果（窓の下に出す）

# IsExtracting は、この機械で吸い出しが動いているかを返します（extract.ps1 が握っている印があるか）。
function IsExtracting {
  $m = $null
  if ([System.Threading.Mutex]::TryOpenExisting('w-cms-onenote-extract', [ref]$m)) { $m.Dispose(); return $true }
  return $false
}

function GetTask { Get-ScheduledTask -TaskName $taskName -ErrorAction SilentlyContinue }

# ResultText は、タスクの前の回の結果を言葉にします（数字は Task Scheduler の SCHED_S_*・SCHED_E_*）。
function ResultText([uint32]$code) {
  switch ($code) {
    0 { return '正常に終わりました' }
    0x41301 { return '動いています' }
    0x41303 { return 'まだ一度も動いていません' }
    0x41306 { return '途中で止められました（[T] で止めたため）' }
    default { return ('結果 0x{0:X}' -f $code) }
  }
}

function StartNow {
  $task = GetTask
  if (-not $task) { $script:note = "この機械にはタスク「$taskName」がありません"; return }
  if ($task.State -eq 'Disabled') { $script:note = 'タスクが無効になっています（タスク スケジューラで「有効」にしてから）'; return }
  if (IsExtracting) { $script:note = 'もう動いています'; return }
  # 「待っています」の回が居ると新しい回は捨てられる（重ねて動かさない設定）——先に取り消す。
  if ($task.State -eq 'Queued') { Stop-ScheduledTask -TaskName $taskName; Start-Sleep -Seconds 1 }
  $svc = New-Object -ComObject Schedule.Service
  $svc.Connect()
  [void]$svc.GetFolder('\').GetTask($taskName).RunEx($null, 2, 0, '') # 2 = TASK_RUN_IGNORE_CONSTRAINTS
  $script:note = (Get-Date).ToString('HH:mm:ss') + ' に始めました（使いながらでも止まりません——止めるときは [T]）'
}

function StopNow {
  $task = GetTask
  if ($task -and ($task.State -eq 'Running' -or $task.State -eq 'Queued')) {
    Stop-ScheduledTask -TaskName $taskName
    if ($task.State -eq 'Running') {
      # 止められた回は記録に終わりの行を書けないので、ここで書く（後で読んで途中で止まった理由が分かるように）。
      [IO.File]::AppendAllText($logFile, (Get-Date).ToString('yyyy-MM-dd HH:mm:ss') + "  様子の画面から止めました`r`n", $utf8)
    }
    $script:note = (Get-Date).ToString('HH:mm:ss') + ' に止めました（次の回が続きから取ります）'
  } elseif (IsExtracting) {
    $script:note = 'タスクの外で動いています（手で流したもの）——その窓を閉じてください'
  } else {
    $script:note = '動いていません'
  }
}

function Show {
  $task = GetTask
  $running = IsExtracting
  Write-Host ("ワンノートの吸い出し（{0}）    {1}" -f $Notebook, (Get-Date).ToString('yyyy-MM-dd HH:mm:ss'))
  Write-Host ''
  if ($running -or ($task -and $task.State -eq 'Running')) {
    Write-Host '  いま:  ● 動いています' -ForegroundColor Green
  } elseif ($task -and $task.State -eq 'Queued') {
    Write-Host '  いま:  … 始まるのを待っています（タスクの条件が合うまで）' -ForegroundColor Yellow
  } elseif ($task -and $task.State -eq 'Disabled') {
    Write-Host '  いま:  × タスクが無効です' -ForegroundColor Red
  } else {
    Write-Host '  いま:  ○ 止まっています'
  }
  if (Test-Path -LiteralPath $machineFile) {
    $m = ([IO.File]::ReadAllText($machineFile, $utf8)).Trim() -split "`t"
    $when = [datetime]::MinValue
    if ($m.Count -ge 2 -and [datetime]::TryParse($m[1], [ref]$when)) {
      Write-Host ('  最後にページを保存: {0}（{1}）' -f $when.ToString('MM-dd HH:mm:ss'), $m[0])
    }
  }
  if ($task) {
    $info = Get-ScheduledTaskInfo -TaskName $taskName
    if ($info.LastRunTime -and $info.LastRunTime.Year -gt 2000) {
      Write-Host ('  前の回: {0} に始まり — {1}' -f $info.LastRunTime.ToString('MM-dd HH:mm'), (ResultText $info.LastTaskResult))
    }
    if ($info.NextRunTime) {
      Write-Host ('  次の回: {0}（タスクの予定）' -f $info.NextRunTime.ToString('MM-dd HH:mm'))
    } else {
      Write-Host '  次の回: 予定はありません（[S] で始めたときだけ動きます）'
    }
  } else {
    Write-Host "  この機械にはタスク「$taskName」がありません（吸い出すのは家の機械）"
  }
  # 通信量（extract.ps1 が吸い出しのあいだ外への口を測って書く・この機械だけの記録）。
  if (Test-Path -LiteralPath $trafficFile) {
    $tj = [IO.File]::ReadAllText($trafficFile, $utf8) | ConvertFrom-Json
    $b = [int64]0
    $day = $tj.days.PSObject.Properties[(Get-Date).ToString('yyyy-MM-dd')]
    if ($day) { $b = [int64]$day.Value }
    $cap = if ($tj.dailyMB -gt 0) { ' / 上限 {0} MB' -f $tj.dailyMB } else { '' }
    Write-Host ('  今日の通信量: {0:N1} MB{1}（吸い出しが動いていたあいだに測った分）' -f ($b / 1000000), $cap)
    if ($running -and $tj.interval -gt 0 -and $tj.lastFileAt) {
      $last = [datetime]$tj.lastFileAt
      Write-Host ('  最後にファイルを書いた: {0}・次は {1} ごろ（{2} 秒おき）' -f $last.ToString('HH:mm:ss'), $last.AddSeconds($tj.interval).ToString('HH:mm:ss'), $tj.interval)
    }
  }
  if (Test-Path -LiteralPath $logFile) {
    Write-Host ''
    Write-Host '  記録の最後（吸い出しの記録.log）:'
    $tail = @(Get-Content -LiteralPath $logFile -Encoding UTF8 -Tail 4)
    foreach ($l in $tail) { Write-Host ('    ' + $l) -ForegroundColor DarkGray }
    # 始めた行で終わっているのに動いていない——途中で止められた回（[T] で止めたか、上限に当たったか、落ちたか）。
    if ($tail.Count -gt 0 -and $tail[-1] -like '*吸い出しを始めます*' -and -not $running) {
      Write-Host '    → 前の回は途中で止まりました。次の回が続きから取ります。' -ForegroundColor Yellow
    }
  }
}

if ($Start) { StartNow; Write-Output $script:note; return }
if ($Stop) { StopNow; Write-Output $script:note; return }
if ($Once) { Show; return }

$Host.UI.RawUI.WindowTitle = 'ワンノートの吸い出しの様子'
while ($true) {
  Clear-Host
  Show
  Write-Host ''
  if ($script:note) { Write-Host ('  ' + $script:note) -ForegroundColor Cyan }
  Write-Host '  [S] いま始める   [T] 止める   [Q] 閉じる     （5秒ごとに新しくします）'
  $until = (Get-Date).AddSeconds(5)
  $key = $null
  while ((Get-Date) -lt $until -and -not $key) {
    if ([Console]::KeyAvailable) { $key = [Console]::ReadKey($true).Key } else { Start-Sleep -Milliseconds 200 }
  }
  switch ($key) {
    'S' { StartNow }
    'T' { StopNow }
    'Q' { return }
  }
}

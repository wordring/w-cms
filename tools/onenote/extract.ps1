# ─────────────────────────────────────────────────────────────────────────
# ワンノートの吸い出し（2026-09-28）——ワンノート → デスクトップの w-cms\ワンノート\<ノートブック>\
#
# 利用者:「ワンノートからデータを少しずつ吸い取り、試験的に使ってみて、仕様の問題を洗い出したい」
# 「一度吸い出したデータはデスクトップのフォルダに格納し、次は差分を吸い取るように出来ますか？」
# 「移行データの製造を吸い出したデータから行うようにして、通信料を節約したい」。
#
#   - 吸い出すセクションは、出力先の「対象.txt」に1行1つ（「グループ / セクション」の末尾が一致すれば対象・
#     「グループ / *」ならそのセクショングループの下を全部）。少しずつ足していく。# で始まる行は注記。
#   - ページごとに page.xml（本文）と、画像（CallbackID で取る）・添付（pathCache の写し）を files\ に置く。
#   - 目録.json に ページID → 最終更新時刻 を残し、**次からは新しい・変わったページだけ**吸い出す。
#     ワンノートから消えたページは目録で「消えた」にする（吸い出した物は消さない）。
#   - 移行データの製造（w-cms へ入れる）はこのフォルダだけを読む——何度やり直してもワンノートに触らない。
#   - 印刷イメージ（図面の PNG）の元の XPS も取る（2026-09-28 利用者:「PNGの図面はワンノートではXPS形式で
#     記録されています。これをベクターのままPDFに変換できませんか？」）。XPS は**束**（何ページもある印刷物）で、
#     同じ束を多くのページが共有するので、XPS\ に束ごと1回だけ置く（名前は idDocument）。
#   - -Wait は、まだこの機械へ降りてきていない画像を待つ秒数（既定60・0なら待たずに次の回へ回す）。
#   - 取れなかったものはページごとに目録の missing と「取れなかったもの.txt」に残し、次の回が取り直す。何をしたかは
#     「吸い出しの記録.log」。-MaxMinutes で1回の時間を区切れる（残りは次の回）。目録は1ページごとに保存し、
#     二重には動かない——時間をおいて繰り返し動かしてよい。
#   - -Interval <秒> でファイルの間を空け、-DailyMB <MB> で1日の通信量を**測って**上限で止める（2026-09-30・下の
#     「通信量を測る」）。
#
# ⚠ **読むだけ**です（GetHierarchy・GetPageContent・GetBinaryPageContent）。書き込み系は使わない。
# ⚠ **32ビットの PowerShell で動かす**（OneNote が32ビット版で、64ビットでは開けない）:
#     C:\Windows\SysWOW64\WindowsPowerShell\v1.0\powershell.exe -NoProfile -ExecutionPolicy Bypass -File extract.ps1 [ノートブック名]
# ⚠ このファイルは BOM 付き UTF-8 で保存すること（PowerShell 5.1 は BOM 無しを cp932 として読む）。
# ⚠ 実データの名前をこのファイルに書かないこと（公開リポジトリ）——対象は出力先の「対象.txt」に書く。
# ─────────────────────────────────────────────────────────────────────────
param([string]$Notebook = '板金部', [int]$Wait = 60, [int]$MaxMinutes = 0, [string]$SkipIfIn = '', [int]$Interval = 0, [int]$DailyMB = 0)
# -SkipIfIn <ノートブック>: そのノートブックに同じものがあるページは取らない（2026-09-28 利用者:「板金部に無いものだけ、
#   〈別のノートブック〉から移植すると良いと思います」——別のノートブックから板金部へコピーして移した経緯があり、重なりが多い）。
#   「同じもの」は ■図面番号 の値か添付の名前が一致すること（先にそのノートブックを吸い出しておく）。⚠ 題だけでは比べない
#   ——「まとめ」のように同じ題が装置ごとにある。一致したページは画像も添付も取らず、目録に skipped で残す（通信の節約）。
# 吸い出しの版——上げると、変わっていないページも1度だけ吸い出し直す（2 で XPS を取るようにした）。
$version = 2
$ErrorActionPreference = 'Stop'
$utf8 = New-Object System.Text.UTF8Encoding($false)
$started = Get-Date

$root = Join-Path ([Environment]::GetFolderPath('Desktop')) ('w-cms\ワンノート\' + $Notebook)
New-Item -ItemType Directory -Force $root | Out-Null
# 記録（タスク スケジューラから無人で回すとき、何をしたかを後で読むため）。
$logFile = Join-Path $root '吸い出しの記録.log'
function Log([string]$s) {
  $line = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss') + '  ' + $s
  Write-Output $s
  [IO.File]::AppendAllText($logFile, $line + "`r`n", $utf8)
}
# ⚠ **二重に動かさない**（2026-09-28 利用者:「ダウンロードできなかったものを記録し、時間をおいて再ダウンロードを
#    試してください。この再ダウンロードプロセスをセッションが止まっても実行することは出来ますか？」——タスク
#    スケジューラで繰り返し動かすので、前の回が長引いたときに重ならないようにする）。
$mutex = New-Object System.Threading.Mutex($false, 'w-cms-onenote-extract')
# ⚠ 前の回が途中で落ちていると、名前つきの印は「持ち主が居なくなった」例外で返る——取れたとみなす。
$own = $false
try { $own = $mutex.WaitOne(0) } catch [System.Threading.AbandonedMutexException] { $own = $true }
if (-not $own) { Log "前の回がまだ動いているので、この回は止めます"; return }
# ⚠ **2台の機械で同じノートブックを吸い出さない**（2026-09-28 利用者:「ワンノートの取り込みを家のパソコンでやって、同時に
#    会社でw-cmsへ取り込むことは出来ますか？」）——出力先は OneDrive で2台に届くので、両方が目録を書くと食い違う。上の印は
#    1台の中でしか効かないので、出力先に「吸い出している機械.txt」（機械の名前と時刻）を置き、**別の機械が1時間以内に
#    吸い出していたら止める**。機械を替えるときは、前の機械のタスクを止めてから1時間待つ（急ぐならこのファイルを消す）。
$machineFile = Join-Path $root '吸い出している機械.txt'
if (Test-Path -LiteralPath $machineFile) {
  $prev = ([IO.File]::ReadAllText($machineFile, $utf8)).Trim() -split "`t"
  $prevTime = [datetime]::MinValue
  if ($prev.Count -ge 2) { [void][datetime]::TryParse($prev[1], [ref]$prevTime) }
  if ($prev[0] -and $prev[0] -ne $env:COMPUTERNAME -and ((Get-Date) - $prevTime).TotalMinutes -lt 60) {
    Log ("別の機械（{0}）が {1} に吸い出しています——この機械（{2}）では止めます（{3} を見てください）" -f `
      $prev[0], $prev[1], $env:COMPUTERNAME, $machineFile)
    $mutex.ReleaseMutex()
    return
  }
}
function MarkMachine { [IO.File]::WriteAllText($machineFile, $env:COMPUTERNAME + "`t" + (Get-Date).ToString('s'), $utf8) }
MarkMachine
$targetFile = Join-Path $root '対象.txt'
if (-not (Test-Path $targetFile)) {
  [IO.File]::WriteAllText($targetFile, "# 吸い出すセクション（1行1つ・「グループ / セクション」の末尾が一致すれば対象・「グループ / *」ならその下を全部）`r`n", $utf8)
  Write-Output "対象.txt を作りました。吸い出すセクションを書いてから、もう一度動かしてください: $targetFile"
  return
}
$targets = @([IO.File]::ReadAllLines($targetFile, $utf8) | ForEach-Object { $_.Trim() } | Where-Object { $_ -and -not $_.StartsWith('#') })
# InTarget は「グループ / セクション」のパスが対象かを返します。行の末尾が一致すれば対象。⚠ 行が「 / *」で終わるなら、その
# セクショングループの下を全部（2026-09-30 利用者:「01 資料、次に02 記録もダウンロードしておきましょうか」——02 記録は
# 年／月／セクションの70個で、書き並べると後から増えるセクションを拾えない）。
function InTarget([string]$path) {
  foreach ($t in $script:targets) {
    if ($t -match '/\s*\*$') { # 「01 資料 / *」も「01 資料/*」も
      $g = ($t -replace '\s*/\s*\*$', '')
      if ($path.StartsWith($g + ' / ') -or $path.Contains(' / ' + $g + ' / ')) { return $true }
    } elseif ($path.EndsWith($t)) { return $true }
  }
  return $false
}
# ワンノートでも失われたもの（2026-09-30）——出力先の「ワンノートでも失われたもの.txt」に、人が画像・XPS の名前を1行1つ書く
# （「取れなかったもの.txt」に出た名前）。取れなくても取り残しに数えず、ページを開いて待つこともしない——取り残しのページは
# 回ごとにワンノートに開かせて待つので、取れないものを待ち続け、そのたびに通信量も数えられていた。目録には lostInOneNote で残し、
# 製造の報告にも出る。利用者:「ワンノートでも表示できなくなっています」。
$lostFile = Join-Path $root 'ワンノートでも失われたもの.txt'
$lostSet = @{}
if (Test-Path -LiteralPath $lostFile) {
  foreach ($ln in [IO.File]::ReadAllLines($lostFile, $utf8)) { $ln = $ln.Trim(); if ($ln -and -not $ln.StartsWith('#')) { $lostSet[$ln] = $true } }
}
if ($targets.Count -eq 0) { Write-Output "対象.txt にセクションがありません: $targetFile"; return }

# 目録（ページID → 記録）
$catalogFile = Join-Path $root '目録.json'
$catalog = @{}
if (Test-Path $catalogFile) {
  $old = [IO.File]::ReadAllText($catalogFile, $utf8) | ConvertFrom-Json
  foreach ($p in $old.pages.PSObject.Properties) { $catalog[$p.Name] = $p.Value }
}

# ── 通信量を測る・ファイルの間を空ける（2026-09-30） ──
# 利用者:「例えば、3分に一回ファイル一つのように、ダウンロードすることはできますか？ウェブのクローラのようにです」
# 「一日の通信量を1GB以内に納めたいです。統計的にではなく測ってです」。
#   - -Interval <秒>: ファイル（画像・XPS・添付——page.xml は数えない）を1つ書くたびに、前のファイルからこの秒数が
#     経つまで待つ。⚠ クラウドから降ろすのはワンノート（ページを開いたときと裏の同期）で、この道具が書いたファイルは
#     OneDrive が上げる——**書く間を空けると、両方がゆっくりになる**。
#   - -DailyMB <MB>: この機械の外への口（Get-NetAdapter -Physical）の受信＋送信を、**吸い出しが動いているあいだ**測って
#     日ごとに足し、その日の分が上限の 50MB 手前に来たら回を止める（1ファイル・1ページぶんは止める前に動いている）。
#     ⚠ 測るのは口の全部——ワンノートが降ろす分・OneDrive が上げる分のほか、**同じ時間に動いた別の通信も入る**
#     （多めに数える側）。吸い出していない時間の通信は数えない。⚠ 測れないときは上限を守れないので回を始めない。
#     記録はこの機械だけのもの（LOCALAPPDATA——OneDrive に置くと、記録そのものを上げ続ける）。様子の画面（status.ps1）が読む。
#   - 待ちを付けた回は長いので、その回が終わるまで Windows にスリープしないよう頼む（画面は消える）。
$trafficFile = Join-Path $env:LOCALAPPDATA 'w-cms\ワンノートの通信量.json'
$lineIds = @()
try { $lineIds = @(Get-NetAdapter -Physical | ForEach-Object { $_.InterfaceGuid.ToUpper() }) } catch { }
if ($lineIds.Count -eq 0) {
  $lineIds = @([System.Net.NetworkInformation.NetworkInterface]::GetAllNetworkInterfaces() | Where-Object {
      ($_.NetworkInterfaceType -eq 'Ethernet' -or $_.NetworkInterfaceType -eq 'Wireless80211') -and
      $_.Description -notmatch 'Virtual|Hyper-V|VPN|TAP' } | ForEach-Object { $_.Id.ToUpper() })
}
$lineLast = @{} # 口ごとの前の値（受信＋送信）
$traffic = @{}  # 日 → バイト
if (Test-Path -LiteralPath $trafficFile) {
  $tj = [IO.File]::ReadAllText($trafficFile, $utf8) | ConvertFrom-Json
  foreach ($p in $tj.days.PSObject.Properties) { $traffic[$p.Name] = [int64]$p.Value }
}
$runBytes = [int64]0
$lastFileAt = [datetime]::MinValue
$trafficSaved = [datetime]::MinValue
$budgetHit = $false
function TodayKey { (Get-Date).ToString('yyyy-MM-dd') }
function TodayBytes { [int64]$script:traffic[(TodayKey)] }
function SaveTraffic([bool]$running = $true) {
  $cut = (Get-Date).AddDays(-60).ToString('yyyy-MM-dd')
  $keep = [ordered]@{}
  foreach ($k in @($script:traffic.Keys | Sort-Object)) { if ($k -ge $cut) { $keep[$k] = $script:traffic[$k] } }
  $o = [PSCustomObject]@{
    machine = $env:COMPUTERNAME; running = $running; interval = $Interval; dailyMB = $DailyMB
    lastFileAt = $(if ($script:lastFileAt -gt [datetime]::MinValue) { $script:lastFileAt.ToString('s') } else { '' })
    savedAt = (Get-Date).ToString('s'); days = [PSCustomObject]$keep
  }
  New-Item -ItemType Directory -Force (Split-Path $trafficFile) | Out-Null
  [IO.File]::WriteAllText($trafficFile, ($o | ConvertTo-Json -Depth 3), $utf8)
  $script:trafficSaved = Get-Date
}
# Meter は外への口の数え口を読み、前に読んだときからの増えた分を今日の分に足します（1分に1回は記録を書く）。
function Meter {
  $today = TodayKey
  foreach ($n in [System.Net.NetworkInformation.NetworkInterface]::GetAllNetworkInterfaces()) {
    $nid = $n.Id.ToUpper()
    if ($script:lineIds -notcontains $nid) { continue }
    $s = $n.GetIPStatistics()
    $cur = [int64]$s.BytesReceived + [int64]$s.BytesSent
    if ($script:lineLast.ContainsKey($nid)) {
      $prev = $script:lineLast[$nid]
      $delta = if ($cur -ge $prev) { $cur - $prev } else { $cur } # 数え直された（つなぎ直した）ら 0 からの値
      $script:traffic[$today] = [int64]$script:traffic[$today] + $delta
      $script:runBytes += $delta
    }
    $script:lineLast[$nid] = $cur
  }
  if (((Get-Date) - $script:trafficSaved).TotalSeconds -ge 60) { SaveTraffic }
}
function OverBudget { $DailyMB -gt 0 -and (TodayBytes) -ge ([int64]$DailyMB * 1000000 - 50000000) }
# Pace はファイルを1つ書く前に呼びます——前のファイルから -Interval 秒経つまで待ち（そのあいだも測る）、書いてよければ
# $true。その日の通信量が上限に来ていれば $false（書かずに、その回を止める印を立てる）。
function Pace {
  while ($true) {
    Meter
    if (OverBudget) { $script:budgetHit = $true; return $false }
    if ($Interval -le 0) { break }
    $left = $Interval - ((Get-Date) - $script:lastFileAt).TotalSeconds
    if ($left -le 0) { break }
    Start-Sleep -Seconds ([int][Math]::Min(10, [Math]::Ceiling($left)))
  }
  $script:lastFileAt = Get-Date
  if ($Interval -gt 0) { SaveTraffic } # 様子の画面が「次のファイル」の時刻を出せるように
  return $true
}
function MB([int64]$b) { '{0:N1} MB' -f ($b / 1000000) }
# SameBytes は、置き場に同じ中身のファイルが既にあれば true——書かない（待ちも数えない）。⚠ 取れていないものがある
# ページは回ごとに丸ごと吸い出し直すので、これが無いと**同じ画像を毎回書き直し、OneDrive が毎回上げ直していた**
# （2026-09-30 に通信量を測って分かった——取り残しの2ページで1回 39 ファイル・約 38 MB）。
function SameBytes([string]$path, [byte[]]$bytes) {
  if (-not (Test-Path -LiteralPath $path)) { return $false }
  if ((Get-Item -LiteralPath $path).Length -ne $bytes.Length) { return $false }
  $sha = [System.Security.Cryptography.SHA256]::Create()
  try { $a = $sha.ComputeHash([IO.File]::ReadAllBytes($path)); $b = $sha.ComputeHash($bytes) } finally { $sha.Dispose() }
  return [Convert]::ToBase64String($a) -eq [Convert]::ToBase64String($b)
}
function SameFile([string]$path, [string]$src) {
  if (-not (Test-Path -LiteralPath $path)) { return $false }
  if ((Get-Item -LiteralPath $path).Length -ne (Get-Item -LiteralPath $src).Length) { return $false }
  return (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -eq (Get-FileHash -LiteralPath $src -Algorithm SHA256).Hash
}
if ($DailyMB -gt 0 -and $lineIds.Count -eq 0) {
  Log "外への口が見つからず通信量を測れないので、この回は止めます（-DailyMB を外すと測らずに動きます）"
  $mutex.ReleaseMutex(); return
}
Meter # 最初の値を取る（ここからの増えた分を数える）
if (OverBudget) {
  Log ("今日の通信量が {0} で上限（{1} MB）に近いので、この回は止めます——明日以降に" -f (MB (TodayBytes)), $DailyMB)
  SaveTraffic $false; $mutex.ReleaseMutex(); return
}
if ($Interval -gt 0) {
  # 長い回のあいだスリープしない（ES_CONTINUOUS | ES_SYSTEM_REQUIRED・この回が終われば解ける）。
  Add-Type -Namespace WCms -Name Power -MemberDefinition '[DllImport("kernel32.dll")] public static extern uint SetThreadExecutionState(uint esFlags);'
  [void][WCms.Power]::SetThreadExecutionState([uint32]2147483649) # 0x80000001
}

function SafeName([string]$s, [int]$max = 50) {
  $t = ($s -replace '[\/:*?"<>|\x00-\x1f]', '_').Trim().TrimEnd('.')
  if ($t.Length -gt $max) { $t = $t.Substring(0, $max) }
  if (-not $t) { $t = '_' }
  return $t
}

$on = New-Object -ComObject OneNote.Application
$xml = ''
$on.GetHierarchy('', 4, [ref]$xml)
[xml]$doc = $xml
$ns = New-Object System.Xml.XmlNamespaceManager($doc.NameTable)
$ns.AddNamespace('one', $doc.DocumentElement.NamespaceURI)
$nb = $doc.SelectSingleNode("//one:Notebook[@name='$Notebook']", $ns)
if ($nb -eq $null) { throw "ノートブック「$Notebook」がありません" }

# SaveCatalog は目録を書きます——**1ページごとに**書く（無人の回が途中で止められても、そこまでは残る）。
function SaveCatalog {
  $out = [PSCustomObject]@{ notebook = $Notebook; updatedAt = (Get-Date).ToString('s'); pages = [PSCustomObject]$catalog }
  [IO.File]::WriteAllText($catalogFile, ($out | ConvertTo-Json -Depth 5), $utf8)
  MarkMachine # 吸い出している機械の時刻も新しく（長い回でも別の機械が割り込まない）
}

$how = "吸い出しを始めます（待ち {0} 秒・上限 {1} 分" -f $Wait, $(if ($MaxMinutes -gt 0) { $MaxMinutes } else { 'なし' })
if ($Interval -gt 0) { $how += "・ファイルの間 $Interval 秒" }
if ($DailyMB -gt 0) { $how += ("・1日の通信 {0} MB まで（今日ここまで {1}）" -f $DailyMB, (MB (TodayBytes))) }
Log ($how + '）')
$seen = @{}
$stopped = $false
$stat = @{ new = 0; changed = 0; same = 0; files = 0; xps = 0; missing = 0; skipped = 0; rekeyed = 0; kept = 0; lostInOneNote = 0 }

# ── 目録の鍵は機械に依らないもの（2026-09-29） ──
# ⚠ ワンノートのページID（GetHierarchy の ID）は**機械ごとに違う**（頭の GUID が別物）——家で吸い出すと、会社で吸い出した
#    ページが全部「新しい」と数えられ、別のIDで二重に入った（09-28 夜に家で手試しして分かった）。ページへのリンク
#    （GetHyperlinkToObject）の page-id={…} はワンノートのファイルの中のページのIDなので、これを鍵にする。取れなければ
#    この機械のIDのまま（報告に出す）。
$stat.nokey = 0
function StableKey([string]$lid) {
  $link = ''
  try { $on.GetHyperlinkToObject($lid, '', [ref]$link) } catch { $link = '' }
  if ($link -match 'page-id=(\{[0-9A-Fa-f-]{36}\})') { return $Matches[1].ToUpper() }
  $script:stat.nokey++
  return $lid
}
# 置き場を2つ以上のページが共有しているもの（09-29 まで・同じ節に同じ題）——その回で新しいフォルダへ吸い出し直す。
$dirUse = @{}
foreach ($r in $catalog.Values) { if ($r.dir) { $dirUse[$r.dir] = 1 + [int]$dirUse[$r.dir] } }
$sharedDirs = @{}
foreach ($d in @($dirUse.Keys)) { if ($dirUse[$d] -gt 1) { $sharedDirs[$d] = $true } }
if ($sharedDirs.Count -gt 0) { Log ("置き場を共有しているフォルダ {0} 個——そのページは新しいフォルダへ吸い出し直します" -f $sharedDirs.Count) }

# ── 同じものを見分ける手掛かり（-SkipIfIn）──
function NormCode([string]$s) {
  if (-not $s) { return '' }
  return ($s.Normalize([Text.NormalizationForm]::FormKC).ToUpper() -replace '[\s\-‐‑‒–—―ー_]', '')
}
function NormName([string]$s) {
  if (-not $s) { return '' }
  return ($s.Normalize([Text.NormalizationForm]::FormKC).ToLower() -replace '\s', '')
}
# KeysOf はページの本文（基本の XML）から ■図面番号 の値と添付の名前を取ります。⚠ 添付の名前は**3桁以上の数字を含む
# ものだけ**（図番・日付入りの名前）——「展開 〈部品名〉.dxf」のような部品名だけの名前は、別の装置の同じ名前の
# 部品（図面番号は違う）にもあり、それで比べると板金部に無い部品を取りこぼす。
function KeysOf([xml]$x) {
  $n2 = New-Object System.Xml.XmlNamespaceManager($x.NameTable)
  $n2.AddNamespace('one', $x.DocumentElement.NamespaceURI)
  $texts = @($x.SelectNodes('//one:T', $n2) | ForEach-Object { (($_.InnerText -replace '<[^>]*>', '') -replace '&nbsp;', ' ').Trim() } | Where-Object { $_ })
  $nos = @()
  for ($i = 0; $i -lt $texts.Count - 1; $i++) {
    if ($texts[$i] -match '^■\s*図面番号') { $v = NormCode $texts[$i + 1]; if ($v) { $nos += $v } }
  }
  $fs = @($x.SelectNodes('//one:InsertedFile', $n2) | ForEach-Object { NormName $_.GetAttribute('preferredName') } | Where-Object { $_ -match '\d{3,}' })
  return @{ nos = $nos; files = $fs }
}
$known = $null
if ($SkipIfIn) {
  $kroot = Join-Path ([Environment]::GetFolderPath('Desktop')) ('w-cms\ワンノート\' + $SkipIfIn)
  $kcat = Join-Path $kroot '目録.json'
  if (-not (Test-Path $kcat)) { throw "「$SkipIfIn」をまだ吸い出していません（先に吸い出してください）: $kcat" }
  $known = @{ nos = @{}; files = @{} }
  $kc = [IO.File]::ReadAllText($kcat, $utf8) | ConvertFrom-Json
  foreach ($kp in $kc.pages.PSObject.Properties) {
    $kd = $kp.Value.dir
    if (-not $kd) { continue }
    $px = Join-Path $kroot ($kd + '\page.xml')
    if (-not (Test-Path -LiteralPath $px)) { continue }
    [xml]$kx = [IO.File]::ReadAllText($px, $utf8)
    $k = KeysOf $kx
    foreach ($v in $k.nos) { $known.nos[$v] = $kp.Value.title }
    foreach ($v in $k.files) { $known.files[$v] = $kp.Value.title }
  }
  Log ("「{0}」にあるもの: 図面番号 {1}・添付の名前 {2}（同じもののページは取らない）" -f $SkipIfIn, $known.nos.Count, $known.files.Count)
}
:sections foreach ($sec in $nb.SelectNodes('.//one:Section', $ns)) {
  if ($sec.GetAttribute('isInRecycleBin') -eq 'true') { continue }
  $groups = @()
  $p = $sec.ParentNode
  while ($p -ne $null -and $p.LocalName -eq 'SectionGroup') {
    if ($p.GetAttribute('isRecycleBin') -eq 'true') { $groups = @('__ごみ箱__'); break }
    $groups = , $p.GetAttribute('name') + $groups; $p = $p.ParentNode
  }
  if ($groups -contains '__ごみ箱__') { continue }
  $path = (@($groups) + $sec.GetAttribute('name')) -join ' / '
  if (-not (InTarget $path)) { continue }

  $secDir = Join-Path $root (SafeName ($path -replace ' / ', '__') 120)
  foreach ($pg in $sec.SelectNodes('one:Page', $ns)) {
    $id = $pg.GetAttribute('ID')   # この機械のページID（ワンノートの呼び出しに使う）
    $key = StableKey $id           # 目録の鍵（機械に依らない）
    $mod = $pg.GetAttribute('lastModifiedTime')
    $title = $pg.GetAttribute('name')
    $seen[$key] = $true
    $rec = $catalog[$key]
    if ($rec -eq $null -and $catalog.ContainsKey($id)) {
      # 前の版の目録（この機械のIDが鍵）——鍵を移し替える（中身はそのまま使う）。
      $rec = $catalog[$id]; $catalog.Remove($id); $catalog[$key] = $rec; $stat.rekeyed++
    }
    if ($rec -ne $null) {
      # この機械のIDも覚えておく（製造の記録が前の鍵で覚えているページを当てるため・機械ごとに1つずつ足す）。
      $ids = @($rec.localIds | Where-Object { $_ }) ; if ($ids -notcontains $id) { $ids += $id }
      $rec | Add-Member -NotePropertyName localIds -NotePropertyValue ([string[]]$ids) -Force
    }
    # 置き場を別のページと共有している（09-29 まで）なら、新しい名前のフォルダへ吸い出し直す。
    $shared = ($rec -ne $null -and $rec.dir -and $sharedDirs.ContainsKey($rec.dir))
    # 前回取れなかったファイルがあるページ（incomplete）は、変わっていなくても吸い出し直す。
    # 別のノートブックと同じとして飛ばしたページ（skipped・置き場が無い）も、変わっていなければそのまま。
    if ($rec -ne $null -and $rec.lastModified -eq $mod -and -not $rec.incomplete -and $rec.v -ge $version -and -not $shared -and
        ($rec.skipped -or (Test-Path (Join-Path $root $rec.dir)))) {
      $stat.same++; continue
    }
    # 1回の時間の上限（-MaxMinutes）——超えたら残りは次の回へ（遅い回線で少しずつ進めるため）。
    if ($MaxMinutes -gt 0 -and ((Get-Date) - $started).TotalMinutes -ge $MaxMinutes) { $stopped = $true; break sections }
    # その日の通信量の上限（-DailyMB）——来ていれば、このページには手を付けずに止める。
    Meter
    if (OverBudget) { $budgetHit = $true }
    if ($budgetHit) { $stopped = $true; break sections }
    # ページの置き場は最初に決めたものを使い続ける（題が変わってもフォルダ名は変えない）。新しい置き場の名前は
    # 「題__ページの鍵の頭8桁」——⚠ 09-29 までは機械のIDの頭8桁で、これは**節の GUID**なので、同じ節に同じ題の
    # ページが2枚あると1つのフォルダを共有していた（家の手試しで見つかった）。
    if ($rec -ne $null -and $rec.dir -and -not $shared) { $rel = $rec.dir }
    else { $rel = (Split-Path $secDir -Leaf) + '\' + (SafeName $title) + '__' + ($key -replace '[{}]', '').Substring(0, 8) }
    $dir = Join-Path $root $rel
    $files = Join-Path $dir 'files'

    # ── 本文の**構造**は基本（0）で取る ──
    # ⚠ 中身つき（piBinaryData＝1）で頼むと、**まだこの機械へ降りてきていない画像を本文から黙って落とす**
    #    （2026-09-28 に踏んだ——写真3枚のページが1枚になった）。構造は必ず基本で取り、画像は別に取る。
    $content = ''
    $on.GetPageContent($id, [ref]$content, 0)
    [xml]$pdoc = $content
    $pns = New-Object System.Xml.XmlNamespaceManager($pdoc.NameTable)
    $pns.AddNamespace('one', $pdoc.DocumentElement.NamespaceURI)
    # 別のノートブックに同じものがあれば、画像も添付も取らない（-SkipIfIn）。
    if ($known -ne $null) {
      $k = KeysOf $pdoc
      $why = ''
      foreach ($v in $k.nos) { if ($known.nos.ContainsKey($v)) { $why = "「$SkipIfIn」の「" + $known.nos[$v] + "」と同じ図面番号"; break } }
      if (-not $why) { foreach ($v in $k.files) { if ($known.files.ContainsKey($v)) { $why = "「$SkipIfIn」の「" + $known.files[$v] + "」と同じ添付"; break } } }
      if ($why) {
        $stat.skipped++
        $catalog[$key] = [PSCustomObject]@{
          title = $title; section = $path; lastModified = $mod; dir = ''
          extractedAt = (Get-Date).ToString('s'); files = @(); gone = $false; incomplete = $false; v = $version
          missing = @(); skipped = $why; localIds = [string[]]@($id)
        }
        SaveCatalog
        continue
      }
    }
    New-Item -ItemType Directory -Force $files | Out-Null
    $got = @()
    $miss = 0
    $lost = @() # 取れなかったもの（目録の missing・取れなかったもの.txt に出す）
    $lostHere = @() # ワンノートでも失われたもの（目録の lostInOneNote・取り直さない）
    # 画像（写真・印刷イメージ）——名前は CallbackID から。**保存する page.xml の Image に wcmsFile="…" を
    # 書き足す**（製造はこれで画像と結ぶ・推し量らない）。
    $pending = @()
    foreach ($img in $pdoc.SelectNodes('//one:Image', $pns)) {
      $cb = $img.SelectSingleNode('one:CallbackID', $pns)
      if ($cb -eq $null) { $miss++; $lost += '画像（CallbackID なし）'; continue }
      $cid = $cb.GetAttribute('callbackID')
      $fmt = $img.GetAttribute('format'); if (-not $fmt) { $fmt = 'png' }
      $name = 'img_' + (SafeName ($cid -replace '[{}]', '') 60) + '.' + $fmt
      $pending += [PSCustomObject]@{ img = $img; cid = $cid; name = $name
        pr = ($img.GetAttribute('isPrintOut') -eq 'true')
        xi = $img.GetAttribute('xpsFileIndex'); pn = $img.GetAttribute('originalPageNumber') }
    }
    # SaveImg は画像を保存して true。⚠ **中身が0バイトなら保存せず false**——まだ降りてきていない画像に
    #    ワンノートが空の中身を返すことがある（2026-09-28・0バイトのファイルを「取れた」と数えていた）。
    function SaveImg($x, [string]$b64) {
      $bytes = [byte[]]@()
      try { $bytes = [Convert]::FromBase64String($b64.Trim()) } catch { return $false }
      if ($bytes.Length -eq 0) { return $false }
      $dest = Join-Path $files $x.name
      if (SameBytes $dest $bytes) { $script:stat.kept++ } # 同じ中身が既にある——書かない
      else {
        if (-not (Pace)) { return $false } # 間を空ける・その日の通信量の上限（取れなかったものとして次の回へ）
        [IO.File]::WriteAllBytes($dest, $bytes)
      }
      $x.img.SetAttribute('wcmsFile', $x.name)
      return $true
    }
    # 取りに行く（CallbackID → 印刷イメージは中身つきの本文から束の位置で）。取れたものを $pending から外す。
    function TryFetch {
      $rest = @()
      foreach ($x in $script:pending) {
        if ($script:budgetHit) { $rest += $x; continue } # 通信量の上限——残りは次の回
        $b64 = ''
        try { $on.GetBinaryPageContent($id, $x.cid, [ref]$b64) } catch { $b64 = '' }
        if ($b64 -and (SaveImg $x $b64)) { $script:got += $x.name; $stat.files++ } else { $rest += $x }
      }
      if (@($rest | Where-Object { $_.pr }).Count -gt 0 -and -not $script:budgetHit) {
        # ⚠ 印刷イメージは GetBinaryPageContent が 0x8004200F でも、中身つきの本文には入っていることがある。
        #    束の中の位置（xpsFileIndex・originalPageNumber）で結ぶ。
        $bin = ''
        try { $on.GetPageContent($id, [ref]$bin, 1) } catch { $bin = '' }
        if ($bin) {
          [xml]$bdoc = $bin
          $bns = New-Object System.Xml.XmlNamespaceManager($bdoc.NameTable)
          $bns.AddNamespace('one', $bdoc.DocumentElement.NamespaceURI)
          $rest2 = @()
          foreach ($x in $rest) {
            $hit = $null
            if ($x.pr) {
              foreach ($bi in $bdoc.SelectNodes('//one:Image[@isPrintOut="true"]', $bns)) {
                if ($bi.GetAttribute('xpsFileIndex') -eq $x.xi -and $bi.GetAttribute('originalPageNumber') -eq $x.pn) { $hit = $bi; break }
              }
            }
            $d = if ($hit) { $hit.SelectSingleNode('one:Data', $bns) } else { $null }
            if ($d -ne $null -and (SaveImg $x $d.InnerText)) { $script:got += $x.name; $stat.files++ } else { $rest2 += $x }
          }
          $rest = $rest2
        }
      }
      $script:pending = $rest
    }
    TryFetch
    # ワンノートでも失われたもの——待たない・取り残しに数えない（1回は取りに行くので、戻ってきていれば取れる）。
    foreach ($x in @($pending | Where-Object { $lostSet.ContainsKey($_.name) })) {
      $lostHere += $(if ($x.pr) { '印刷イメージ ' } else { '画像 ' }) + $x.name
    }
    $pending = @($pending | Where-Object { -not $lostSet.ContainsKey($_.name) })
    if ($pending.Count -gt 0 -and $Wait -gt 0 -and -not $budgetHit) {
      # ⚠ **まだ降りてきていない画像**——ワンノートにそのページを開かせて降ろさせる（読むだけ・画面が
      #    そのページへ動く）。遅い回線では時間がかかるので、-Wait 秒待って取れなければ次の回に回す（incomplete）。
      try { $on.NavigateTo($id, '', $false) } catch { }
      $until = (Get-Date).AddSeconds($Wait)
      while ($pending.Count -gt 0 -and (Get-Date) -lt $until -and -not $budgetHit) { Start-Sleep -Seconds 3; Meter; TryFetch }
    }
    $miss += $pending.Count
    foreach ($x in $pending) { $lost += $(if ($x.pr) { '印刷イメージ ' } else { '画像 ' }) + $x.name }
    # 印刷イメージの元の XPS（束）。**束ごと1回だけ** XPS\ に置き、page.xml の XPSFile に wcmsFile="…" を
    # 書き足す（製造は印刷イメージの xpsFileIndex で束を引き、originalPageNumber でページを切り出す）。
    $xpsDir = Join-Path $root 'XPS'
    foreach ($xf in $pdoc.SelectNodes('//one:XPSFile', $pns)) {
      $cb = $xf.SelectSingleNode('one:CallbackID', $pns)
      $docID = ($xf.GetAttribute('idDocument') -replace '[{}]', '')
      if ($cb -eq $null -or -not $docID) { $miss++; $lost += 'XPS（CallbackID なし）'; continue }
      $xname = 'xps_' + (SafeName $docID 60) + '.xps'
      $xpath = Join-Path $xpsDir $xname
      if (-not (Test-Path -LiteralPath $xpath)) {
        $b64 = ''
        try { $on.GetBinaryPageContent($id, $cb.GetAttribute('callbackID'), [ref]$b64) } catch { $b64 = '' }
        $bytes = [byte[]]@()
        if ($b64) { try { $bytes = [Convert]::FromBase64String($b64.Trim()) } catch { } }
        if ($bytes.Length -eq 0) {
          if ($lostSet.ContainsKey($xname)) { $lostHere += 'XPS ' + $xname } else { $miss++; $lost += 'XPS ' + $xname }
          continue
        }
        if (-not (Pace)) { $miss++; $lost += 'XPS ' + $xname; continue } # 間を空ける・通信量の上限
        New-Item -ItemType Directory -Force $xpsDir | Out-Null
        [IO.File]::WriteAllBytes($xpath, $bytes)
        $stat.xps++
      }
      $xf.SetAttribute('wcmsFile', $xname)
    }
    $pxml = Join-Path $dir 'page.xml'
    if (-not (Test-Path -LiteralPath $pxml) -or [IO.File]::ReadAllText($pxml, $utf8) -ne $pdoc.OuterXml) { # 同じなら書かない
      [IO.File]::WriteAllText($pxml, $pdoc.OuterXml, $utf8)
    }
    # 添付（PDF・CAD 等）——pathCache に実体がある（pathSource は元の置き場で、もう無いことがある）。
    foreach ($f in $pdoc.SelectNodes('//one:InsertedFile | //one:MediaFile', $pns)) {
      $src = $f.GetAttribute('pathCache')
      if (-not $src -or -not (Test-Path -LiteralPath $src)) { $miss++; $lost += '添付 ' + $f.GetAttribute('preferredName'); continue }
      $oid = ($f.GetAttribute('objectID') -replace '[{}]', '')
      $name = 'att_' + (SafeName $oid 40) + '__' + (SafeName $f.GetAttribute('preferredName') 80)
      $dest = Join-Path $files $name
      if (SameFile $dest $src) { $stat.kept++ } # 同じ中身が既にある——写さない
      else {
        if (-not (Pace)) { $miss++; $lost += '添付 ' + $f.GetAttribute('preferredName'); continue } # 間を空ける・通信量の上限
        Copy-Item -LiteralPath $src -Destination $dest -Force
      }
      $got += $name; $stat.files++
    }
    $stat.missing += $miss
    $stat.lostInOneNote += $lostHere.Count
    if ($rec -eq $null) { $stat.new++ } else { $stat.changed++ }
    $catalog[$key] = [PSCustomObject]@{
      title = $title; section = $path; lastModified = $mod; dir = $rel
      extractedAt = (Get-Date).ToString('s'); files = $got; gone = $false; incomplete = ($miss -gt 0); v = $version
      missing = @($lost); lostInOneNote = @($lostHere); localIds = [string[]]$(if ($rec -ne $null) { @($rec.localIds) } else { @($id) })
    }
    SaveCatalog
  }
}
# 対象のセクションから消えたページ（吸い出した物は残し、印だけ付ける）——⚠ 時間の上限で途中で止めた回は
# 全部を見ていないので付けない（見ていないページを「消えた」にしてしまう）。
$gone = 0
if (-not $stopped) {
  foreach ($k in @($catalog.Keys)) {
    $r = $catalog[$k]
    if ((InTarget $r.section) -and -not $seen.ContainsKey($k) -and -not $r.gone) { $r.gone = $true; $gone++ }
  }
}
SaveCatalog
# 取れなかったもの（ページごと）——次の回が取り直す。人が読む一覧。
$left = @($catalog.Values | Where-Object { $_.incomplete -and -not $_.gone } | Sort-Object section, title)
# ⚠ 先頭の要素は括弧で包む——PowerShell では「,」が「+」より先に結び付き、後ろの行まで1つの文字列に繋がる。
$lines = @(('# 取れなかったもの（' + (Get-Date).ToString('yyyy-MM-dd HH:mm') + ' 時点・' + $left.Count + ' ページ）'),
  '# 次の吸い出しで取り直します。ワンノートでそのページを開くと早く降りてきます。', '')
foreach ($r in $left) {
  $lines += $r.section + ' / ' + $r.title
  foreach ($m in @($r.missing)) { if ($m) { $lines += '    ' + $m } }
}
$gaveUp = @($catalog.Values | Where-Object { @($_.lostInOneNote | Where-Object { $_ }).Count -gt 0 -and -not $_.gone } | Sort-Object section, title)
if ($gaveUp.Count -gt 0) {
  $lines += ''
  $lines += ('# ワンノートでも失われたもの（' + $gaveUp.Count + ' ページ）——取り直しません（ワンノートでも失われたもの.txt に書いたもの）')
  foreach ($r in $gaveUp) {
    $lines += $r.section + ' / ' + $r.title
    foreach ($m in @($r.lostInOneNote)) { if ($m) { $lines += '    ' + $m } }
  }
}
[IO.File]::WriteAllText((Join-Path $root '取れなかったもの.txt'), ($lines -join "`r`n") + "`r`n", $utf8)
$summary = "新しい {0}・変わった {1}・同じ {2}・消えた {3}・ファイル {4}・XPS {7}・取れなかった {5}・まだ取れていないページ {8}・同じものがあるので取らなかった {9}・鍵を移し替えた {10}・鍵が取れなかった {11}・同じ中身なので書かなかった {12}・ワンノートでも失われた {13}" -f `
  $stat.new, $stat.changed, $stat.same, $gone, $stat.files, $stat.missing, $root, $stat.xps, $left.Count, $stat.skipped, $stat.rekeyed, $stat.nokey, $stat.kept, $stat.lostInOneNote
Meter
if ($DailyMB -gt 0 -or $Interval -gt 0) { $summary += ("・通信 {0}（今日 {1}）" -f (MB $runBytes), (MB (TodayBytes))) }
if ($budgetHit) { $summary += ("（今日の通信量が上限 {0} MB に近づいたので止めました——残りは明日以降の回）" -f $DailyMB) }
elseif ($stopped) { $summary += '（時間の上限で止めました——残りは次の回）' }
Log $summary
SaveTraffic $false
if ($Interval -gt 0) { [void][WCms.Power]::SetThreadExecutionState([uint32]2147483648) } # 0x80000000——スリープしてよい
$mutex.ReleaseMutex()

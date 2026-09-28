# ─────────────────────────────────────────────────────────────────────────
# ワンノートの吸い出し（2026-09-28）——ワンノート → デスクトップの w-cms\ワンノート\<ノートブック>\
#
# 利用者:「ワンノートからデータを少しずつ吸い取り、試験的に使ってみて、仕様の問題を洗い出したい」
# 「一度吸い出したデータはデスクトップのフォルダに格納し、次は差分を吸い取るように出来ますか？」
# 「移行データの製造を吸い出したデータから行うようにして、通信料を節約したい」。
#
#   - 吸い出すセクションは、出力先の「対象.txt」に1行1つ（「グループ / セクション」の末尾が一致すれば対象）。
#     少しずつ足していく。# で始まる行は注記。
#   - ページごとに page.xml（本文）と、画像（CallbackID で取る）・添付（pathCache の写し）を files\ に置く。
#   - 目録.json に ページID → 最終更新時刻 を残し、**次からは新しい・変わったページだけ**吸い出す。
#     ワンノートから消えたページは目録で「消えた」にする（吸い出した物は消さない）。
#   - 移行データの製造（w-cms へ入れる）はこのフォルダだけを読む——何度やり直してもワンノートに触らない。
#
# ⚠ **読むだけ**です（GetHierarchy・GetPageContent・GetBinaryPageContent）。書き込み系は使わない。
# ⚠ **32ビットの PowerShell で動かす**（OneNote が32ビット版で、64ビットでは開けない）:
#     C:\Windows\SysWOW64\WindowsPowerShell\v1.0\powershell.exe -NoProfile -ExecutionPolicy Bypass -File extract.ps1 [ノートブック名]
# ⚠ このファイルは BOM 付き UTF-8 で保存すること（PowerShell 5.1 は BOM 無しを cp932 として読む）。
# ⚠ 実データの名前をこのファイルに書かないこと（公開リポジトリ）——対象は出力先の「対象.txt」に書く。
# ─────────────────────────────────────────────────────────────────────────
param([string]$Notebook = '板金部')
$ErrorActionPreference = 'Stop'
$utf8 = New-Object System.Text.UTF8Encoding($false)

$root = Join-Path ([Environment]::GetFolderPath('Desktop')) ('w-cms\ワンノート\' + $Notebook)
New-Item -ItemType Directory -Force $root | Out-Null
$targetFile = Join-Path $root '対象.txt'
if (-not (Test-Path $targetFile)) {
  [IO.File]::WriteAllText($targetFile, "# 吸い出すセクション（1行1つ・「グループ / セクション」の末尾が一致すれば対象）`r`n", $utf8)
  Write-Output "対象.txt を作りました。吸い出すセクションを書いてから、もう一度動かしてください: $targetFile"
  return
}
$targets = @([IO.File]::ReadAllLines($targetFile, $utf8) | ForEach-Object { $_.Trim() } | Where-Object { $_ -and -not $_.StartsWith('#') })
if ($targets.Count -eq 0) { Write-Output "対象.txt にセクションがありません: $targetFile"; return }

# 目録（ページID → 記録）
$catalogFile = Join-Path $root '目録.json'
$catalog = @{}
if (Test-Path $catalogFile) {
  $old = [IO.File]::ReadAllText($catalogFile, $utf8) | ConvertFrom-Json
  foreach ($p in $old.pages.PSObject.Properties) { $catalog[$p.Name] = $p.Value }
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

$seen = @{}
$stat = @{ new = 0; changed = 0; same = 0; files = 0; missing = 0 }
foreach ($sec in $nb.SelectNodes('.//one:Section', $ns)) {
  if ($sec.GetAttribute('isInRecycleBin') -eq 'true') { continue }
  $groups = @()
  $p = $sec.ParentNode
  while ($p -ne $null -and $p.LocalName -eq 'SectionGroup') {
    if ($p.GetAttribute('isRecycleBin') -eq 'true') { $groups = @('__ごみ箱__'); break }
    $groups = , $p.GetAttribute('name') + $groups; $p = $p.ParentNode
  }
  if ($groups -contains '__ごみ箱__') { continue }
  $path = (@($groups) + $sec.GetAttribute('name')) -join ' / '
  $hit = $false
  foreach ($t in $targets) { if ($path.EndsWith($t)) { $hit = $true; break } }
  if (-not $hit) { continue }

  $secDir = Join-Path $root (SafeName ($path -replace ' / ', '__') 120)
  foreach ($pg in $sec.SelectNodes('one:Page', $ns)) {
    $id = $pg.GetAttribute('ID')
    $mod = $pg.GetAttribute('lastModifiedTime')
    $title = $pg.GetAttribute('name')
    $seen[$id] = $true
    $rec = $catalog[$id]
    # 前回取れなかったファイルがあるページ（incomplete）は、変わっていなくても吸い出し直す。
    if ($rec -ne $null -and $rec.lastModified -eq $mod -and -not $rec.incomplete -and (Test-Path (Join-Path $root $rec.dir))) {
      $stat.same++; continue
    }
    # ページの置き場は最初に決めたものを使い続ける（題が変わってもフォルダ名は変えない）。
    if ($rec -ne $null -and $rec.dir) { $rel = $rec.dir }
    else { $rel = (Split-Path $secDir -Leaf) + '\' + (SafeName $title) + '__' + ($id -replace '[{}]', '').Substring(0, 8) }
    $dir = Join-Path $root $rel
    $files = Join-Path $dir 'files'
    New-Item -ItemType Directory -Force $files | Out-Null

    # ── 本文の**構造**は基本（0）で取る ──
    # ⚠ 中身つき（piBinaryData＝1）で頼むと、**まだこの機械へ降りてきていない画像を本文から黙って落とす**
    #    （2026-09-28 に踏んだ——写真3枚のページが1枚になった）。構造は必ず基本で取り、画像は別に取る。
    $content = ''
    $on.GetPageContent($id, [ref]$content, 0)
    [xml]$pdoc = $content
    $pns = New-Object System.Xml.XmlNamespaceManager($pdoc.NameTable)
    $pns.AddNamespace('one', $pdoc.DocumentElement.NamespaceURI)
    $got = @()
    $miss = 0
    # 画像（写真・印刷イメージ）——名前は CallbackID から。**保存する page.xml の Image に wcmsFile="…" を
    # 書き足す**（製造はこれで画像と結ぶ・推し量らない）。
    $pending = @()
    foreach ($img in $pdoc.SelectNodes('//one:Image', $pns)) {
      $cb = $img.SelectSingleNode('one:CallbackID', $pns)
      if ($cb -eq $null) { $miss++; continue }
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
      [IO.File]::WriteAllBytes((Join-Path $files $x.name), $bytes)
      $x.img.SetAttribute('wcmsFile', $x.name)
      return $true
    }
    # 取りに行く（CallbackID → 印刷イメージは中身つきの本文から束の位置で）。取れたものを $pending から外す。
    function TryFetch {
      $rest = @()
      foreach ($x in $script:pending) {
        $b64 = ''
        try { $on.GetBinaryPageContent($id, $x.cid, [ref]$b64) } catch { $b64 = '' }
        if ($b64 -and (SaveImg $x $b64)) { $script:got += $x.name; $stat.files++ } else { $rest += $x }
      }
      if (@($rest | Where-Object { $_.pr }).Count -gt 0) {
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
    if ($pending.Count -gt 0) {
      # ⚠ **まだ降りてきていない画像**——ワンノートにそのページを開かせて降ろさせる（読むだけ・画面が
      #    そのページへ動く）。遅い回線では時間がかかるので、1分待って取れなければ次の回に回す（incomplete）。
      try { $on.NavigateTo($id, '', $false) } catch { }
      $until = (Get-Date).AddSeconds(60)
      while ($pending.Count -gt 0 -and (Get-Date) -lt $until) { Start-Sleep -Seconds 3; TryFetch }
    }
    $miss += $pending.Count
    [IO.File]::WriteAllText((Join-Path $dir 'page.xml'), $pdoc.OuterXml, $utf8)
    # 添付（PDF・CAD 等）——pathCache に実体がある（pathSource は元の置き場で、もう無いことがある）。
    foreach ($f in $pdoc.SelectNodes('//one:InsertedFile | //one:MediaFile', $pns)) {
      $src = $f.GetAttribute('pathCache')
      if (-not $src -or -not (Test-Path -LiteralPath $src)) { $miss++; continue }
      $oid = ($f.GetAttribute('objectID') -replace '[{}]', '')
      $name = 'att_' + (SafeName $oid 40) + '__' + (SafeName $f.GetAttribute('preferredName') 80)
      Copy-Item -LiteralPath $src -Destination (Join-Path $files $name) -Force
      $got += $name; $stat.files++
    }
    $stat.missing += $miss
    if ($rec -eq $null) { $stat.new++ } else { $stat.changed++ }
    $catalog[$id] = [PSCustomObject]@{
      title = $title; section = $path; lastModified = $mod; dir = $rel
      extractedAt = (Get-Date).ToString('s'); files = $got; gone = $false; incomplete = ($miss -gt 0)
    }
  }
}
# 対象のセクションから消えたページ（吸い出した物は残し、印だけ付ける）
$gone = 0
foreach ($k in @($catalog.Keys)) {
  $r = $catalog[$k]
  $inTarget = $false
  foreach ($t in $targets) { if ($r.section.EndsWith($t)) { $inTarget = $true; break } }
  if ($inTarget -and -not $seen.ContainsKey($k) -and -not $r.gone) { $r.gone = $true; $gone++ }
}
$out = [PSCustomObject]@{ notebook = $Notebook; updatedAt = (Get-Date).ToString('s'); pages = [PSCustomObject]$catalog }
[IO.File]::WriteAllText($catalogFile, ($out | ConvertTo-Json -Depth 5), $utf8)
Write-Output ("新しい {0}・変わった {1}・同じ {2}・消えた {3}・ファイル {4}・取れなかった {5} → {6}" -f `
  $stat.new, $stat.changed, $stat.same, $gone, $stat.files, $stat.missing, $root)

# copy-backup.ps1 — 業務サーバーの、暗号化した控えを事務所の PC（HP405）へ写す（2026-10-26・毎日の Windows のタスク）。
#
# 利用者:「過去のデータが全部なくなるのはマズいです」——サーバー機の外の控えは、事務所の別の PC と社外の両方。写す PC は HP405。
# 形は「暗号化したまま・毎朝の Windows のタスク・読むことしかできない絞った鍵」（利用者の答え）。
#
#   - 業務サーバーの利用者 wcmspc の絞った鍵（~\.ssh\id_ed25519_wcms_pc）で、w-cms-pcrepo-export に一覧を聞き、増えたファイルだけを取る。
#   - 置き場は C:\w-cms-控え\restic-pc（OneDrive の外——業務のデータを OneDrive へ上げない）。中身は restic で暗号化したまま。
#   - restic のファイルは、名前が中身の SHA-256 なので、取ってきたら指紋で壊れていないかを確かめる（config だけは名前が決まっている）。
#   - 写し終えたら業務サーバーに一言残す（done）。毎朝の監査 w-cms-audit が「HP405 の写し」として読む。
#   - 記録は C:\w-cms-控え\写した記録.log。
#
# ⚠ このファイルは BOM 付きの UTF-8 で保存する（PowerShell 5.1 は BOM 無しを cp932 として読み、日本語が崩れる）。
$ErrorActionPreference = 'Stop'
$root = 'C:\w-cms-控え'
$repo = Join-Path $root 'restic-pc'
$log = Join-Path $root '写した記録.log'
$key = Join-Path $env:USERPROFILE '.ssh\id_ed25519_wcms_pc'
$server = 'wcmspc@192.168.1.100'
$sshOpts = @('-i', $key, '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'ConnectTimeout=20')

function Write-Log([string]$m) {
	"$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss') $m" | Add-Content -Path $log -Encoding UTF8
}

New-Item -ItemType Directory -Force -Path $repo | Out-Null
try {
	$lines = @(& ssh @sshOpts $server list)
	if ($LASTEXITCODE -ne 0) { throw "一覧を取れません（ssh の終わりの番号 $LASTEXITCODE）" }
	$got = 0
	foreach ($line in $lines) {
		if ([string]::IsNullOrWhiteSpace($line)) { continue }
		$size, $path = $line -split "`t", 2
		if ($path -notmatch '^(config|(keys|snapshots|index)/[0-9a-f]{64}|data/[0-9a-f]{2}/[0-9a-f]{64})$') { throw "知らない道です: $path" }
		$dest = Join-Path $repo ($path -replace '/', '\')
		# config（控えの置き場の鍵の入れ物）は、一度写したら替えない（2026-10-27 の見直し）——中身の指紋で確かめられない唯一の
		# ファイルなので、乗っ取られた業務サーバーが別の config を渡すと、この PC の控えを開けなくできた。
		if ($path -eq 'config' -and (Test-Path $dest)) { continue }
		if ((Test-Path $dest) -and ((Get-Item $dest).Length -eq [int64]$size)) { continue }
		New-Item -ItemType Directory -Force -Path (Split-Path $dest) | Out-Null
		$tmp = "$dest.part"
		# 中身はそのまま受ける——PowerShell 5.1 のパイプや > は文字に直してバイナリを壊すので、cmd の > を使う。
		cmd /c "ssh -i `"$key`" -o BatchMode=yes -o IdentitiesOnly=yes -o ConnectTimeout=20 $server get $path > `"$tmp`""
		if ($LASTEXITCODE -ne 0) { throw "取れません: $path" }
		if ((Get-Item $tmp).Length -ne [int64]$size) { throw "大きさが合いません: $path" }
		if ($path -ne 'config') {
			$want = Split-Path $path -Leaf
			$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $tmp).Hash.ToLower()
			if ($hash -ne $want) { throw "指紋が合いません（壊れて届いた）: $path" }
		}
		Move-Item -Force -LiteralPath $tmp -Destination $dest
		$got++
	}
	& ssh @sshOpts $server done $got | Out-Null
	Write-Log "写しました（取ってきたファイル $got・置き場のファイル $($lines.Count)）"
	Write-Output "写しました（取ってきたファイル $got・置き場のファイル $($lines.Count)）"
} catch {
	Write-Log "⚠ 写せませんでした: $_"
	Write-Output "⚠ 写せませんでした: $_"
	exit 1
}

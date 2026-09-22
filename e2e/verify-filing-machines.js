// 整理パネルの「装置名称の候補」（2026-09-11）。
//
// 顧客名で解いたのと同じ問題が一段下に残っていた——1通のメールの5枚が
// `φ410 2輪` / `2輪シュート改良` / `φ410-2輪` / `2軸シュート改良`（輪→軸の誤読）に
// 割れ、そのまま流すと1台の装置が4フォルダに散る。
//
// **検証用に作ったページは最後に片付けます**（実データに残骸を積まない）。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
// ⚠ **当て先は焼き込みません**（2026-09-18）。`000001` と書いてあったので、データを
// 入れ直してテンプレート置き場が `000001` になった日から、下ごしらえが
// **テンプレート領域の中**に作られていました——あそこは索引に載らない決まりなので、
// 提案が常に空になり、整理のボタンが出ませんでした（**黙って通る壊れ方**）。
// 通信箱は走るときに探します（`lib.findMailbox` と同じ「トップ直下・題が通信箱」）。
let MAILBOX = process.env.WCMS_MAILBOX || '';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  ✓ ' : '  ✗ ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await lib.login(page, BASE);

  // ── 下ごしらえ: 通信箱の下に仮の記録ページ、その下に図面ブロックを持つ加工製品ページ
  if (!MAILBOX) {
    const top = await lib.childrenOf(page, '000000');
    MAILBOX = ((top || []).find(c => (c.Title || '').trim() === '通信箱') || {}).ID || '';
    if (!MAILBOX) { console.log('通信箱がありません（管理画面の「置き場」で作れます）'); process.exit(1); }
  }

  const api = (path, opts) => page.evaluate(async ([p, o]) => {
    const res = await fetch(p, o || {});
    return { status: res.status, text: await res.text() };
  }, [path, opts]);

  const mk = async (parent) => {
    const r = await page.evaluate(async (pid) => {
      const fd = new URLSearchParams(); fd.set('parent', pid);
      const res = await fetch('/api/new-page', { method: 'POST', body: fd });
      return { status: res.status, url: res.url };
    }, parent);
    const m = /\/(\d{6})/.exec(r.url || '');
    return m ? m[1] : null;
  };
  const save = async (id, html) => page.evaluate(async ([pid, body]) => {
    const lk = await (await fetch('/api/lock?id=' + pid, { method: 'POST' })).json();
    const res = await fetch('/api/save', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ page_id: pid, html: body, token: lk.token }),
    });
    await fetch('/api/unlock?id=' + pid, { method: 'POST' });
    return res.status;
  }, [id, html]);

  const recordID = await mk(MAILBOX);
  const partID = await mk(recordID);
  if (!recordID || !partID) { console.log('下ごしらえに失敗しました'); process.exit(1); }
  // **整理ボタンは通信記録にしか出ません**（`チャネル` タグで判定）。年フォルダや
  // 加工製品ページに出しても行き場がないため（2026-09-03 ユーザー指摘）。
  await save(recordID, '<h1>[検証用] 装置名称の候補</h1>'
    + '<dl data-type="tags"><dt>チャネル</dt><dd>メール</dd>'
    + '<dt>向き</dt><dd>受信</dd></dl>');
  await save(partID,
    // ⚠ **ヘッダは可変タグ**（2026-09-18 に素の定義リストから移した）。
    // 素の `<dl>` はもうDBに入りません——「タグと表だけがDBに入る」。
    '<h1>[検証用] 加工製品</h1><section data-id="tst1"><h2>図面</h2>' +
    '<dl data-type="tags">' +
    '<dt>図面番号</dt><dd>TEST-1</dd><dt>図面名称</dt><dd>[検証用] 部品</dd>' +
    '<dt>装置名称</dt><dd>2輪シュート改良</dd>'
    + '<dt>客先</dt><dd>南北スポーツ機械</dd></dl></section>');

  try {
    await page.goto(BASE + '/' + recordID);
    // 下ごしらえが効いているかを先に見る（ボタンは提案が1件以上あるときだけ出る）。
    // **前提の確認**——提案が空なら下ごしらえが効いていません（当て先の取り違え・
    // 索引に載らない領域など）。ボタンの待ちで固まるより、理由を出して落ちるほうがよい。
    const proposal = await api('/api/filing-proposal?page_id=' + recordID);
    if (!/"rows":\[\{/.test(proposal.text)) {
      console.log('  ✗ 下ごしらえの提案が空です（通信箱=' + MAILBOX + '）:',
        proposal.status, proposal.text.slice(0, 160));
      process.exit(1);
    }
    await page.waitForSelector('.filing-open', { timeout: 5000 });
    await page.click('.filing-open');
    await page.waitForSelector('.filing-panel', { timeout: 5000 });

    const r = await page.evaluate(() => {
      const panel = document.querySelector('.filing-panel');
      const cust = panel.querySelector('input[aria-label="customer"]');
      const mach = panel.querySelector('input[aria-label="machine_name"]');
      const listID = mach.getAttribute('list');
      const dl = listID ? document.getElementById(listID) : null;
      return {
        custList: cust.getAttribute('list'),
        machList: listID,
        custValue: cust.value,
        options: dl ? Array.from(dl.options).map(o => o.value) : null,
      };
    });
    ok(!!r.machList, '装置名称の欄に候補リストが付いている', r.machList);
    ok(r.custValue === '南北スポーツ機械', '顧客名の推奨値が入っている', r.custValue);
    // ⚠ **候補はその顧客のページが在って初めて出ます**（推奨値は解析が読んだ名前でも
    // 入りますが、装置は木を辿って集めるため）。連絡先を1件も登録していない環境では
    // 顧客ページが無いので、**壊れたのではなく前提が無い**——飛ばします（2026-09-16）。
    const hasCustomerPage = await page.evaluate(async (name) => {
      const top = await (await fetch('/api/children?parent_id=000000')).json().catch(() => []);
      const box = (top || []).find(c => (c.Title || '').trim() === '取引先');
      if (!box) return false;
      const kids = await (await fetch('/api/children?parent_id=' + box.ID)).json().catch(() => []);
      return (kids || []).some(c => (c.Title || '').trim() === name);
    }, r.custValue);
    if (hasCustomerPage) {
      ok(r.options && r.options.length > 0, 'その顧客の装置が候補に出る', (r.options || []).join(' / '));
    } else {
      console.log('  — 顧客ページ（' + r.custValue + '）がまだありません。装置の候補はこの環境では出ないので飛ばします');
    }

    // **候補が「段の下に在るもの」だけであること**を、木を辿って確かめます。
    //
    // もとは `φ410 2輪` という**実データの装置名を決め打ち**にしていました。
    // データを入れ直すと消えるうえ（2026-09-13 に実際に消えた）、**混ざりものを
    // 見逃します**——2026-09-14 には `担当者` の下の人名（`小澤 美智子` 等）が
    // 候補に並んでいて、名前の決め打ちでは「無い」としか分かりませんでした。
    const legit = await page.evaluate(async (opts) => {
      const kids = async (id) => {
        const res = await fetch('/api/children?parent_id=' + id);
        return res.ok ? (await res.json() || []) : [];
      };
      const d = await (await fetch('/api/tag-schema')).json();
      // 段の一覧は設定の持ち物。ここでも手書きしない。
      const box = (await kids('000000')).find(p => p.Title === '取引先');
      if (!box) return { ok: false, why: '取引先ページがありません' };
      const cust = (await kids(box.ID)).find(p => p.Title === '南北スポーツ機械');
      if (!cust) return { ok: false, why: '顧客ページがありません' };
      const stages = await kids(cust.ID);
      const machines = [];
      const others = [];
      for (const s of stages) {
        const names = (await kids(s.ID)).map(p => p.Title);
        // 段の下＝装置、それ以外の箱（担当者など）の下＝装置ではない。
        (['現行', '旧型', '試作'].includes(s.Title) ? machines : others).push(...names);
      }
      return {
        ok: true,
        stray: opts.filter(o => others.includes(o) && !machines.includes(o)),
        missing: machines.filter(m => !opts.includes(m)),
      };
    }, r.options || []);
    if (!legit.ok) {
      console.log('  — 木を辿れませんでした（' + legit.why + '）。この項目は飛ばします');
    } else {
      ok(legit.stray.length === 0, '段の下に無いもの（担当者の人名など）が混じらない',
         legit.stray.join(' / '));
      ok(legit.missing.length === 0, '段の下の装置はすべて候補にある', legit.missing.join(' / '));
    }

    // 顧客名を別の会社へ打ち替えると、候補が入れ替わる（空になる）
    await page.fill('.filing-panel input[aria-label="customer"]', '知らない会社');
    await page.waitForTimeout(200);
    const after = await page.evaluate(() => {
      const mach = document.querySelector('.filing-panel input[aria-label="machine_name"]');
      const dl = document.getElementById(mach.getAttribute('list'));
      return Array.from(dl.options).map(o => o.value);
    });
    ok(after.length === 0, '顧客を打ち替えると候補も追随する', '候補 ' + after.length + '件');

    // 戻すと復活する
    await page.fill('.filing-panel input[aria-label="customer"]', '南北スポーツ機械');
    await page.waitForTimeout(200);
    const back = await page.evaluate(() => {
      const mach = document.querySelector('.filing-panel input[aria-label="machine_name"]');
      return Array.from(document.getElementById(mach.getAttribute('list')).options).map(o => o.value);
    });
    if (hasCustomerPage) {
      ok(back.length > 0, '顧客を戻すと候補も戻る', back.join(' / '));
    } else {
      console.log('  — 同上（顧客ページが無いので候補は空のまま）');
    }
    ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
  } finally {
    // ── 片付け（子から先に消す。子持ちは削除できない）
    for (const id of [partID, recordID]) {
      // **削除には編集ロックが要ります**（handler_delete.go）。取らずに投げると409。
      // **`/api/lock` では取り直せません**——同じ利用者でも、別の口から握った
      // ロックは `ok:false, same_user:true` で断られます。片付けは強制取得で。
      // 応答がJSONで返らないこともあるので、素のテキストから拾います。
      const res = await page.evaluate(async (pid) => {
        let token = '';
        try {
          const lr = await fetch('/api/lock/force?id=' + pid, { method: 'POST' });
          const body = await lr.text();
          const m = /"token"\s*:\s*"([^"]+)"/.exec(body);
          if (m) token = m[1];
        } catch (e) { /* 取れなければトークン無しで試す */ }
        const r = await fetch('/api/delete-page?id=' + pid + '&token=' + token, { method: 'POST' });
        return { status: r.status, text: (await r.text()).slice(0, 60) };
      }, id);
      console.log('  片付け ' + id + ': ' + (res.status === 200 ? '削除' : res.status + ' ' + res.text));
    }
  }
  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();

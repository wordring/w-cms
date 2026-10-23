// 製造ページの作業（2026-10-21 夕・ext/toho/mfg_work.go・mfg_view.go・toho.js の mfgBarOf）を画面から確かめる。
//
// 利用者:「一つの製造ページに案件の見積もり依頼、部材発注、製造記録（工数など）、納品書、請求書を並べるのはどうですか？」
// 「2．勧め通りで」（製造完了は製造ページで付け、案件明細の製造完了は鏡）。
// 2026-10-21 利用者:「製造明細表を作業開始という見出しの下にいれ、行の先頭にチェックボックスを付け、選択した行について,」
// 「表の下にあるボタンを押すとその作業を開始するマナーにしたいです。」
//
//   ⓪ 新しい製造ページに見出し「作業開始」・製造明細表の行の先頭にチェック・表の下に帯（表の中にボタンは無い）
//   ① 製造明細の行を選んで「✓ 製造完了」——本文の欄に「済」
//   ② 案件残表の「進み」に 製造完了 8/20（製造完了の製造に入ったのは8）・案件ページの製造完了の欄にも 8/20 の鏡
//   ③ 行を選んで「🚚 納品作業」——「納品作業」の見出しの下に納品表（まだ納めていない 8）
//   ④ 納品表の数量を 5 に直して「🚚 納品書を作る」——納品書は 記録／年／月／製造記録 にでき（数量 5）、「この製造の書類」に並ぶ・納品表は消える
//   ⑤ もう一度「🚚 納品作業」——納品表は 残りの 3
//   ⑥ 行を選んで「🧾 部材作業」——「部材作業」の見出しの下に必要部材表（材料 2個/台 × 8台 = 16）
//   ⑦ 必要部材表の行を選んで「📨 見積もり」——「見積依頼作業」の見出しの下に見積依頼部材表（ロット 8・案件つき）・必要部材表は残る
//   ⑧ 必要部材表の行を選んで「🛒 発注」——「発注作業」の見出しの下に発注部材表（16）・必要部材表は消える
//   ⑨ 見出し5つがそろい、どの表の中にもボタンが無い
//   ⑩ 発注部材表の下の欄で「部材発注書を作る」——部材発注書は記録にでき、明細の「製造」の列にこの製造ページ・「この製造の書類」に並ぶ（差出人が無ければ飛ばす）
//   ⑪ 行を選んで「🧮 見積もり計算作業」——見積計算書が 記録／年／月／製造記録 にできて開き、「この製造の書類」に並ぶ
//   ⑫ ページの末尾の「📁 この製造を閉じる」——記録／年／月／製造記録へ移り、足元に「閉じています」
//
// 当て先は全部自分で作って最後に消します（案件残表を置いたページ・案件ページ・製造ページと、記録に置いた書類——「この製造の書類」から拾う）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mfg-page.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const parentOf = (page, id) => page.evaluate(async (pid) => (await (await fetch('/api/page-meta?id=' + pid)).json()).parent_id, id);
const titleOf = (page, id) => page.evaluate(async (pid) => ((await (await fetch('/api/load?id=' + pid)).text()).match(/<h1[^>]*>([^<]*)</) || [, ''])[1], id);
const bodyOf = (page, id) => page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
// 本文から、キャプションが caption の表を切り出す（無ければ空）。
const tableIn = (body, caption) => {
  const at = body.indexOf('<caption>' + caption + '</caption>');
  return at < 0 ? '' : body.slice(at, body.indexOf('</table>', at));
};
// 見出し h2「head」が、キャプション caption の表より前にあるか。
const under = (body, head, caption) => {
  const h = body.indexOf('<h2>' + head + '</h2>');
  const t = body.indexOf('<caption>' + caption + '</caption>');
  return h >= 0 && t > h;
};
// 製造ページの「この製造の書類」に並ぶページ番号（2026-10-21・ext/toho/mfg_docs.go）。
const docsOf = (page, id) => page.evaluate(async (pid) => {
  const v = await (await fetch('/api/load?view=1&id=' + pid)).text();
  // 書類は作業の見出しごとに分かれて並ぶ（見積計算書は 2026-10-23 から「見積もり計算作業」の下）ので、全部の欄から集める。
  const out = [];
  for (let at = v.indexOf('class="mfg-docs"'); at >= 0; at = v.indexOf('class="mfg-docs"', at + 1)) {
    for (const m of v.slice(at, v.indexOf('</div>', at)).matchAll(/href="\/(\d{6})"/g)) out.push(m[1]);
  }
  return out;
}, id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', order = '', mfg = '', dlv = '', product = '';
  const docs = new Set(); // 記録に置いた書類（最後に消す）

  // 製造ページを開き、picks に当たるチェックを全部入れて、btn を押す（押すと読み直すか、別のページへ移る）。
  const press = async (picks, btn, opts = {}) => {
    await page.goto(BASE + '/' + mfg);
    const b = page.locator('#w-editor-content ' + btn);
    await b.first().waitFor({ timeout: 10000 }).catch(() => {});
    if (picks) for (const c of await page.locator('#w-editor-content ' + picks).all()) await c.check();
    if (opts.dialog !== undefined) page.once('dialog', (d) => d.accept(opts.dialog));
    await Promise.all([
      opts.url ? page.waitForURL(opts.url, { timeout: 15000 }).catch(() => {}) : page.waitForNavigation({ timeout: opts.wait || 15000 }).catch(() => {}),
      b.first().click().catch(() => {}),
    ]);
  };

  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】製造ページの帯</h1><section data-mirror="案件残"></section>');
    product = await makePage(page, '<h1>【E2E】製造ページの品物</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl><table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>【E2E】鉄</td><td>FB</td><td>t9*99*999</td><td>2</td></tr></tbody></table>', root);
    order = await makePage(page, '<h1>【E2E】製造ページの案件</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-MP-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】製造ページの客先</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>状態</th><th>製造完了</th></tr>' +
      '<tr><td>' + product + '</td><td>E2E-MP-A</td><td>【E2E】部品</td><td>20</td><td>個</td><td>未着手</td><td></td></tr></tbody></table>', root);
    const made = await page.evaluate(async (oid) => {
      const r = await fetch('/api/mfg/new', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: [{ order: oid, row: 0, item: 'E2E-MP-A', qty: '8' }] }) });
      return r.json();
    }, order);
    mfg = made.page_id || '';
    check('当て先を作れた', !!root && !!order && !!mfg, JSON.stringify(made));

    // ⓪
    await page.goto(BASE + '/' + mfg);
    await page.locator('#w-editor-content .mfg-items-bar').waitFor({ timeout: 10000 }).catch(() => {});
    const b0 = await bodyOf(page, mfg);
    check('⓪ 新しい製造ページに見出し「作業開始」（製造明細表の前）', under(b0, '作業開始', '製造明細表'));
    const s0 = await page.evaluate(() => {
      const bar = document.querySelector('#w-editor-content .mfg-items-bar');
      // ⚠ エディタはブロックごとに包むので、隣り合わせではなく文書の順で見る（表 → 帯 → 次の表・見出し）。
      const all = Array.from(document.querySelectorAll('#w-editor-content table, #w-editor-content h2, #w-editor-content .mfg-items-bar'));
      const i = all.indexOf(bar);
      const prev = i > 0 ? all[i - 1] : null;
      return {
        picks: document.querySelectorAll('#w-editor-content table input.mfg-pick').length,
        barAfterTable: !!prev && prev.tagName === 'TABLE' && !!prev.querySelector('caption') && prev.querySelector('caption').textContent.trim() === '製造明細表',
        buttons: bar ? Array.from(bar.querySelectorAll('button')).map((x) => x.className.replace('chip-btn ', '')).join(' ') : '',
        inTable: document.querySelectorAll('#w-editor-content table button').length,
      };
    });
    check('⓪ 製造明細表の行の先頭にチェック', s0.picks === 1, String(s0.picks));
    check('⓪ 表の下に帯（部材作業・納品・製造完了・見積計算書・製造フォルダへ戻す）', s0.barAfterTable &&
      ['mfg-act-parts', 'mfg-act-dlv', 'mfg-act-made', 'mfg-act-calc', 'mfg-act-back'].every((c) => s0.buttons.includes(c)), s0.buttons);
    // 2026-10-23: 「➕ 追加の発注」と「製造完了: N / M 行」を外し、「🧾 部材」を「🧾 部材作業」に、「↩ 取り消し」を「↩ 製造フォルダへ戻す」に。
    const barText = await page.evaluate(() => (document.querySelector('#w-editor-content .mfg-items-bar') || {}).textContent || '');
    check('⓪ 帯に「追加の発注」「製造完了: 」が無く、新しい名前になっている', !s0.buttons.includes('mfg-act-extra') && !barText.includes('製造完了: ') &&
      barText.includes('🧾 部材作業') && barText.includes('↩ 製造フォルダへ戻す'), barText.replace(/\s+/g, ' '));
    check('⓪ 表の中にボタンは無い', s0.inTable === 0, String(s0.inTable));
    check('⓪ ページの末尾に「この製造の書類」と「閉じる」', (await page.locator('#w-editor-content .mfg-page-end .mfg-docs').count()) === 1 &&
      (await page.locator('#w-editor-content .mfg-page-end .mfg-close-go').count()) === 1);
    // 何も選ばずに押すと、選ぶように言う。
    await page.click('#w-editor-content .mfg-act-made');
    await page.waitForTimeout(300);
    const say0 = (await page.locator('#w-editor-content .mfg-items-bar .mfg-bar-say').textContent().catch(() => '')) || '';
    check('⓪ 何も選ばずに押すと、選ぶように言う', say0.includes('選んでください'), say0);

    // ①
    await press('.mfg-pick', '.mfg-act-made');
    const raw = await bodyOf(page, mfg);
    check('① 「✓ 製造完了」で、本文の欄に「済」', /<td>\s*済/.test(raw.replace(/<input[^>]*>/g, '')), raw.replace(/\s+/g, ' ').slice(0, 400));

    // ②
    await page.goto(BASE + '/' + root);
    await page.waitForSelector('#w-editor-content tr[data-order-page="' + order + '"]', { timeout: 10000 }).catch(() => {});
    const bl = await page.evaluate((oid) => {
      // 2026-10-21 から、製造完了は「進み」の列に文で出る（「製造完了 8/20」）。
      const tr = document.querySelector('#w-editor-content tr[data-order-page="' + oid + '"]');
      return tr ? tr.textContent.replace(/\s+/g, ' ').trim() : null;
    }, order);
    check('② 案件残表の「進み」に 製造完了 8/20', (bl || '').includes('製造完了 8/20'), String(bl));
    const view = await page.evaluate(async (pid) => (await fetch('/api/load?view=1&id=' + pid)).text(), order);
    const m = view.match(/class="vocab-chrome made-mirror"[^>]*>([^<]*)</);
    check('② 案件ページの製造完了の欄にも 8/20 の鏡', !!m && m[1] === '8/20', m ? m[1] : '');

    // ③
    await press('.mfg-pick', '.mfg-act-dlv');
    let b = await bodyOf(page, mfg);
    let t = tableIn(b, '納品表');
    check('③ 「納品作業」の見出しの下に納品表（まだ納めていない 8）', under(b, '納品作業', '納品表') && t.includes('E2E-MP-A') && />\s*8\s*</.test(t), t.replace(/\s+/g, ' ').slice(0, 400));

    // ④ 数量を 5 に直して（画面の表のセル）、「納品書を作る」。
    await page.goto(BASE + '/' + mfg);
    await page.locator('#w-editor-content .mfg-dlvdraft-make').waitFor({ timeout: 10000 }).catch(() => {});
    const edited = await page.evaluate(() => {
      const table = Array.from(document.querySelectorAll('#w-editor-content table')).find((x) => x.querySelector('caption') && x.querySelector('caption').textContent.trim() === '納品表');
      if (!table) return false;
      const rows = Array.from(table.querySelectorAll('tr'));
      const i = Array.from(rows[0].children).map((c) => c.textContent.trim()).indexOf('数量');
      if (i < 0 || !rows[1] || !rows[1].children[i]) return false;
      rows[1].children[i].textContent = '5';
      return true;
    });
    check('④ 納品表の数量を 5 に直せた', edited);
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + mfg), { timeout: 15000 }).catch(() => {}),
      page.click('#w-editor-content .mfg-dlvdraft-make'),
    ]);
    dlv = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    check('④ 納品書ページが開く', !!dlv && dlv !== mfg, page.url());
    if (dlv) {
      docs.add(dlv);
      check('④ 納品書は 記録／年／月／製造記録', (await titleOf(page, await parentOf(page, dlv))) === '製造記録');
      const db = await bodyOf(page, dlv);
      const at = db.indexOf('E2E-MP-A');
      const row = at < 0 ? '' : db.slice(db.lastIndexOf('<tr', at), db.indexOf('</tr>', at));
      check('④ 納品書の数量は直した 5', />\s*5\s*</.test(row) && !/>\s*8\s*</.test(row), row.replace(/\s+/g, ' '));
      check('④ 製造ページの「この製造の書類」に納品書', (await docsOf(page, mfg)).includes(dlv));
      check('④ 納品表は消えた', !(await bodyOf(page, mfg)).includes('<caption>納品表</caption>'));
    }

    // ⑤
    await press('.mfg-pick', '.mfg-act-dlv');
    t = tableIn(await bodyOf(page, mfg), '納品表');
    check('⑤ もう一度「🚚 納品作業」で、納品表は残りの 3', t.includes('E2E-MP-A') && />\s*3\s*</.test(t), t.replace(/\s+/g, ' ').slice(0, 400));

    // ⑥
    await press('.mfg-pick', '.mfg-act-parts');
    b = await bodyOf(page, mfg);
    t = tableIn(b, '必要部材表');
    check('⑥ 「部材作業」の見出しの下に必要部材表（【E2E】鉄 FB を 16）', under(b, '部材作業', '必要部材表') && t.includes('【E2E】鉄') && />\s*16\s*</.test(t), t.replace(/\s+/g, ' ').slice(0, 600));
    await page.goto(BASE + '/' + mfg);
    await page.locator('#w-editor-content .mfg-needs-bar').waitFor({ timeout: 10000 }).catch(() => {});
    const s6 = await page.evaluate(() => ({
      picks: document.querySelectorAll('#w-editor-content table input.mfg-needs-pick').length,
      buttons: Array.from(document.querySelectorAll('#w-editor-content .mfg-needs-bar button')).map((x) => x.className.replace('chip-btn ', '')).join(' '),
    }));
    check('⑥ 必要部材表の行の先頭にチェック・表の下に 発注・見積もり・不要・外す', s6.picks === 1 &&
      ['mfg-needs-order', 'mfg-needs-quote', 'mfg-needs-skip', 'mfg-needs-drop'].every((c) => s6.buttons.includes(c)), JSON.stringify(s6));

    // ⑦
    await press('.mfg-needs-pick', '.mfg-needs-quote');
    b = await bodyOf(page, mfg);
    t = tableIn(b, '見積依頼部材表');
    check('⑦ 「見積依頼作業」の見出しの下に見積依頼部材表（【E2E】鉄 FB・ロット 8・案件つき）', under(b, '見積依頼作業', '見積依頼部材表') &&
      t.includes('【E2E】鉄') && />\s*8\s*</.test(t) && t.includes(order), t.replace(/\s+/g, ' ').slice(0, 600));
    check('⑦ 必要部材表は残る', tableIn(b, '必要部材表').includes('【E2E】鉄'));

    // ⑧
    await press('.mfg-needs-pick', '.mfg-needs-order');
    b = await bodyOf(page, mfg);
    t = tableIn(b, '発注部材表');
    check('⑧ 「発注作業」の見出しの下に発注部材表（【E2E】鉄 FB を 16）', under(b, '発注作業', '発注部材表') && t.includes('【E2E】鉄') && />\s*16\s*</.test(t), t.replace(/\s+/g, ' ').slice(0, 600));
    check('⑧ 必要部材表は消えた', !b.includes('<caption>必要部材表</caption>'));

    // ⑨
    check('⑨ 見出し5つ（作業開始・部材作業・発注作業・見積依頼作業・納品作業）', ['作業開始', '部材作業', '発注作業', '見積依頼作業', '納品作業'].every((h) => b.includes('<h2>' + h + '</h2>')));
    await page.goto(BASE + '/' + mfg);
    await page.locator('#w-editor-content .draft-form').waitFor({ timeout: 10000 }).catch(() => {});
    const inTable = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content table button')).map((x) => {
      const cap = x.closest('table').querySelector('caption');
      return (cap ? cap.textContent.trim() : '?') + ':' + x.textContent.trim();
    }));
    check('⑨ どの表の中にもボタンが無い（製造明細表・納品表・見積依頼部材表・発注部材表）', inTable.length === 0, inTable.join(' | '));

    // ⑩
    const signer = page.locator('#w-editor-content .draft-form select[data-unorder="signer"] option');
    if ((await signer.count()) < 2) {
      console.log('- ⑩ 差出人（署名のある人）が連絡帳に無いので、部材発注書を作る所は飛ばします');
    } else {
      await page.selectOption('#w-editor-content .draft-form select[data-unorder="signer"]', { index: 1 });
      await page.fill('#w-editor-content .draft-form input[data-draft="supplier"]', '【E2E】部材の業者');
      await Promise.all([
        page.waitForNavigation({ timeout: 20000 }).catch(() => {}),
        page.click('#w-editor-content .draft-form [data-draft-go]'),
      ]);
      const listed = await docsOf(page, mfg);
      let po = '';
      for (const d of listed) {
        docs.add(d);
        if ((await titleOf(page, d)).includes('【E2E】部材の業者')) po = d;
      }
      check('⑩ 製造ページの「この製造の書類」に部材発注書', !!po, listed.join(','));
      if (po) {
        check('⑩ 部材発注書は 記録／年／月／製造記録', (await titleOf(page, await parentOf(page, po))) === '製造記録');
        const body10 = await bodyOf(page, po);
        check('⑩ 部材発注明細表の「製造」にこの製造ページ', body10.includes('<th>製造</th>') && body10.includes('>' + mfg + '<'), body10.replace(/\s+/g, ' ').slice(-500));
        check('⑩ 製造ページの発注部材表は消えた', !(await bodyOf(page, mfg)).includes('<caption>発注部材表</caption>'));
      }
    }

    // ⑪
    await press('.mfg-pick', '.mfg-act-calc', { url: (u) => !u.pathname.endsWith('/' + mfg) });
    const calc = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    if (calc && calc !== mfg) docs.add(calc);
    check('⑪ 「🧮 見積もり計算作業」で見積計算書ができて開く・記録／年／月／製造記録', !!calc && calc !== mfg && (await titleOf(page, await parentOf(page, calc))) === '製造記録', page.url());
    if (calc && calc !== mfg) check('⑪ 製造ページの「この製造の書類」に見積計算書', (await docsOf(page, mfg)).includes(calc));
    if (calc && calc !== mfg) { // 2026-10-23 から、見積計算書は見出し「見積もり計算作業」（納品作業の下）の書類に並ぶ
      const v = await page.evaluate(async (pid) => (await fetch('/api/load?view=1&id=' + pid)).text(), mfg);
      const h = v.search(/>\s*見積もり計算作業\s*<\/h2>/), d = v.search(/>\s*納品作業\s*<\/h2>/);
      check('⑪ 見積計算書は「見積もり計算作業」の下（納品作業の下の見出し）', d >= 0 && h > d && v.indexOf('href="/' + calc + '"', h) > h, 'h=' + h + ' d=' + d);
    }

    // ⑫ 「📁 この製造を閉じる」（2026-10-19・mfg_close.go）——確かめのダイアログで「はい」、記録／年／月／製造記録へ移る。
    await press(null, '.mfg-page-end .mfg-close-go', { dialog: '' });
    const p12 = await parentOf(page, mfg);
    const t12 = await titleOf(page, p12);
    check('⑫ 閉じると 記録／年／月／製造記録 へ移る', t12 === '製造記録', String(p12) + ' ' + t12);
    const v12 = await page.evaluate(async (pid) => (await fetch('/api/load?view=1&id=' + pid)).text(), mfg);
    check('⑫ 閉じた製造ページの足元に「閉じています」・「取り消し」は出ない', v12.includes('この製造は閉じています') && !v12.includes('mfg-close-go') && !v12.includes('mfg-act-back'));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (mfg) for (const d of await docsOf(page, mfg).catch(() => [])) docs.add(d);
    for (const d of docs) await deletePage(page, d).catch(() => {});
    if (mfg) for (const k of await childrenOf(page, mfg).catch(() => [])) await deletePage(page, k.ID || k.id || k).catch(() => {});
    if (dlv) await deletePage(page, dlv).catch(() => {});
    if (mfg) await deletePage(page, mfg).catch(() => {});
    if (product) await deletePage(page, product).catch(() => {});
    if (order) await deletePage(page, order).catch(() => {});
    if (root) await deletePage(page, root).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();

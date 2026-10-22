// ファイル表示の枠：既定の高さ・つまんでの伸縮・高さの記憶（2026-09-10／2026-09-15 に書き直し）
//
// ⚠ **2026-09-15 に全面的に書き直しました。** それまでは
//   ① 当て先が固定のページID（`/010268`）で、データを入れ直すと別のページになり
//   ② 見る先が `.drawing-inline` で、これは 2026-09-14 の作り直しで**消えた class**
//   でした。①だけが知られていて「当て先を差し替えれば直る」とされていましたが、
//   **差し替えても通りません**——②が残るからです。
//
// いまは**自分で当て先を2枚作って、最後に消します**。固定IDに頼らないので、
// データを入れ直しても落ちません。
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const lib = require('./lib');
// **当て先は走るときに探します**（2026-09-16）——ページIDも添付IDもデータを入れ直す
// たびに変わるため（詳しくは lib.js の冒頭）。環境変数を渡せば探索を飛ばします。
let HOST = process.env.WCMS_HOST_PAGE || '';
let ATTACH = process.env.WCMS_ATTACH || '';
let fail = 0;
function ok(c, m, extra) { console.log((c ? '  ✓ ' : '  ✗ ') + m + (extra ? '  ' + extra : '')); if (!c) fail++; }

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await lib.login(page, BASE);
  if (!HOST || !ATTACH) {
    const rec = await lib.findRecordWithPDF(page);
    HOST = HOST || rec.pageID; ATTACH = ATTACH || rec.attachID;
  }
  ok(!!HOST && !!ATTACH, '当て先を見つけた（PDFを持つ通信記録）', HOST + '-' + ATTACH);
  if (!HOST || !ATTACH) { console.log('通信箱にPDF付きの記録がありません'); await browser.close(); process.exit(1); }

  // ── 当て先を2枚作る（高さの記憶が「端末ごと・ページをまたぐ」ことを見るため）
  async function makePage() {
    const url = await page.evaluate(async () => {
      const res = await fetch('/api/new-page', {
        method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: 'parent=000000',
      });
      return res.url;
    });
    const id = (url.match(/\/(\d{6})/) || [])[1];
    if (!id) throw new Error('ページを作れません: ' + url);
    await page.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      await fetch('/api/save', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          page_id: arg.id,
          html: '<h1>枠の大きさ</h1><section data-type="file-view" data-ref="' + arg.ref + '"></section>',
          token: lj.token || '',
        }),
      });
      // **握ったロックは必ず外します**——残すと編集モードにも削除にも入れません。
      await fetch('/api/lock/force?id=' + arg.id, { method: 'POST' });
    }, { id, ref: HOST + '-' + ATTACH });
    return id;
  }

  const ids = [];
  try {
    ids.push(await makePage(), await makePage());
    console.log('    当て先: ' + ids.join(' / '));

    // 記録の置き場は `wcms.view`（物ごと・2026-09-28〜）。古い `wcms.ui` も消してから始める（2026-10-14）。
    await page.evaluate(() => { try { localStorage.removeItem('wcms.ui'); localStorage.removeItem('wcms.view'); } catch (e) {} });
    await page.goto(BASE + '/' + ids[0]); await page.waitForTimeout(2500);

    const a = await page.evaluate(() => {
      const w = document.querySelector('#w-editor-content .file-view');
      const e = w && w.querySelector('embed');
      if (!w) return null;
      const s = getComputedStyle(w);
      return {
        h: Math.round(w.getBoundingClientRect().height), resize: s.resize,
        src: e ? e.getAttribute('src') : null, title: w.title,
        embedH: e ? Math.round(e.getBoundingClientRect().height) : 0,
      };
    });
    console.log('■ 既定');
    ok(!!a, 'ファイル表示の枠がある');
    if (!a) throw new Error('枠が出ていません');
    ok(a.resize === 'vertical', 'つまんで縦に変えられる', a.resize);
    ok(/navpanes=0&view=FitH/.test(a.src || ''), 'サムネイル欄を閉じ幅に合わせている（ツールバーは残す）',
       (a.src || '').split('#')[1]);
    ok(a.title.length > 0, '操作の手掛かり（tooltip）がある');
    ok(a.h > 400, '既定の高さは 70vh 相当', a.h + 'px');
    ok(a.embedH > 0 && a.embedH < a.h, 'つまみの余地を残して PDF が収まっている',
       '枠' + a.h + ' / PDF' + a.embedH);

    // ── つまんで伸ばす
    console.log('■ つまんで伸ばす');
    // **つまみが画面の中に来るまでスクロールします**——枠は700px あるので、
    // そのままだと下端が表示領域の外で、マウスが届きません（1度これで空振りしました）。
    await page.locator('#w-editor-content .file-view').scrollIntoViewIfNeeded();
    await page.evaluate(() => window.scrollBy(0, 120));
    await page.waitForTimeout(300);
    const box = await page.locator('#w-editor-content .file-view').boundingBox();
    console.log('    つまみの位置 y=' + Math.round(box.y + box.height) + ' / 表示領域 1000');
    await page.mouse.move(box.x + box.width - 6, box.y + box.height - 6);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width - 6, box.y + box.height + 250, { steps: 12 });
    await page.mouse.up();
    await page.waitForTimeout(400);
    // ⚠ 憶える形は「物ごと（ページ×添付）の縦横比」（2026-10-14 に試験を追いつかせた）——09-28〜29 から、記録は
    //    `wcms.view` の pages[ページ番号].r['f:'+添付の参照]（高さ÷幅）で、`wcms.ui` の `drawing.height`（全部の枠で
    //    1つの高さ）は使わない（app.js の wireFileViewResize・ViewState）。
    const b = await page.evaluate((arg) => {
      const w = document.querySelector('#w-editor-content .file-view');
      const r = w.getBoundingClientRect();
      const v = JSON.parse(localStorage.getItem('wcms.view') || '{}');
      const p = (v.pages || {})[arg.id] || {};
      return {
        h: Math.round(r.height), w: r.width,
        ratio: (p.r || {})['f:' + arg.ref],
        pe: getComputedStyle(w.querySelector('embed')).pointerEvents,
      };
    }, { id: ids[0], ref: HOST + '-' + ATTACH });
    ok(b.h > a.h + 150, '高さが伸びた', a.h + ' → ' + b.h + 'px');
    ok(typeof b.ratio === 'number' && Math.abs(b.ratio * b.w - b.h) <= 2, 'この枠の縦横比を憶えた',
       'ratio=' + b.ratio + '（' + (typeof b.ratio === 'number' ? Math.round(b.ratio * b.w) + 'px' : '-') + '）');
    ok(b.pe !== 'none', '離したらPDFへの操作が戻る', b.pe);

    // ── 開き直しても同じ高さか
    console.log('■ 開き直す');
    await page.goto(BASE + '/' + ids[0]); await page.waitForTimeout(2500);
    const c = await page.evaluate(() => Math.round(
      document.querySelector('#w-editor-content .file-view').getBoundingClientRect().height));
    ok(Math.abs(c - b.h) <= 2, '前の高さで開く', c + 'px');

    // ── 別のページには効かないか（記憶は物ごと——同じ添付を指していても、別のページの枠は既定の高さのまま）
    //    ⚠ 2026-10-14 に期待を逆にした——09-29 まで記憶は端末に1つで、ページをまたいで効いた（「別のページも同じ高さ」）。
    //    1枚を変えるとほかの PDF まで変わったので、物ごとの縦横比だけになった（利用者:「一枚のPDFの大きさを変えると、
    //    ページを再読み込みすると同じページのほかのPDFの大きさも変わってしまいます」）。
    await page.goto(BASE + '/' + ids[1]); await page.waitForTimeout(2500);
    const d = await page.evaluate(() => Math.round(
      document.querySelector('#w-editor-content .file-view').getBoundingClientRect().height));
    ok(Math.abs(d - a.h) <= 2, '別のページは既定の高さのまま（記憶は物ごと）', d + 'px');
  } finally {
    for (const i of ids) {
      const del = await page.evaluate(async (id) => {
        await fetch('/api/lock/force?id=' + id, { method: 'POST' });
        const res = await fetch('/api/delete-page?force=1&id=' + encodeURIComponent(id), { method: 'POST' });
        return res.status;
      }, i);
      console.log('    後始末: 削除 ' + del + '（' + i + '）');
    }
  }

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();

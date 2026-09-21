// 表の折り返しと横スクロールを確かめる（2026-09-21）
//
// ユーザー:「備考欄以外は改行しないようにして、テーブルが横に大きくなりますから、
// テーブルだけスクロールに出来ますか？」
//
// ここで固定するのは3つ:
//   ① 備考**以外**は1行に保つ（`white-space: nowrap`）
//   ② 備考は折り返す。⚠ **潰れない**（ほかが折り返さないので、下限が無いと
//      表は備考だけを細らせて収めようとする——実測で69pxまで細った）
//   ③ 表は自分の中で横スクロールし、**ページは横へ揺れない**
//
// ⚠ **閲覧モードで見ます。** 印を付ける `validateTypedTables` は 2026-09-21 まで
// **編集モードに入るときだけ**呼ばれていて、開き直すと折り返しの印が消えていました。
// 「一度編集へ入ると直る」という分かりにくい壊れ方なので、ここは閲覧で測ります。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-table-wrap.js
const { chromium } = require('playwright');
const { login } = require('./lib');

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// findBacklogPage は受注残表を置いたページを**走るときに探します**
// （⚠ 当て先を焼き込まない・2026-09-16 の決定）。
async function findBacklogPage(page, base) {
  const children = await page.evaluate(async () => {
    const r = await fetch('/api/children?parent_id=000000', { credentials: 'same-origin' });
    const d = await r.json();
    // ⚠ **応答は配列で、鍵は大文字の `ID`**（`{children:[...]}` ではありません）。
    return Array.isArray(d) ? d.map((c) => c.ID || c.id) : [];
  });
  for (const id of children) {
    if (!id) continue;
    await page.goto(base + '/' + id);
    if (await page.locator('.backlog-table').count() > 0) return '/' + id;
  }
  return '';
}

// 実データに合わせた行——⚠ **品名が20字を超えています**。2026-09-01〜09-21 は
// 「20字まで1行」というしきい値だったので、ここが折り返していました。
const LONG_NAME = '受けブラケット組立て用ロングプレート';
const LONG_NOTE = '材質変更の連絡あり。先方の指示で板厚を変更。次回以降もこの指示が続くかは未確認です。';

const BODY = '<h1>表の折り返し</h1>' +
  '<table data-type="client-order-items"><caption>受注明細</caption><tbody>' +
  '<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th>' +
  '<th>単価</th><th>納期</th><th>出荷済み</th><th>備考</th><th>状態</th></tr>' +
  '<tr><td>000123</td><td>K120-01-211</td><td>' + LONG_NAME + '</td><td>100</td><td>個</td>' +
  '<td>390</td><td>2026-10-15</td><td></td><td>' + LONG_NOTE + '</td><td>未着手</td></tr>' +
  '</tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await login(page, BASE);

  // 当て先は自分で作って、最後に消す（実データを汚さない・入れ直しで落ちない）。
  const url = await page.evaluate(async () => {
    const res = await fetch('/api/new-page', {
      method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'parent=000000',
    });
    return res.url;
  });
  const id = (url.match(/\/(\d{6})/) || [])[1];
  if (!id) { console.log('NG ページを作れません: ' + url); process.exit(1); }

  let bad = 0;
  try {
    await page.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      await fetch('/api/save', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }),
      });
      // **握ったロックは必ず外します**——残すと削除にも入れません。
      await fetch('/api/lock/force?id=' + arg.id, { method: 'POST' });
    }, { id, html: BODY });

    for (const width of [1280, 760, 390]) {
      await page.setViewportSize({ width, height: 900 });
      await page.goto(BASE + '/' + id);
      await page.waitForTimeout(1200); // 印が付くのを待つ（buildToc → decorateVocabBlocks）

      const r = await page.evaluate(() => {
        const t = document.querySelector('#w-editor-content table[data-type]');
        if (!t) return null;
        const row = t.querySelectorAll('tr')[1];
        const cells = Array.from(row.children);
        const head = Array.from(t.querySelectorAll('tr')[0].children).map(c => c.textContent.trim());
        const noteAt = head.indexOf('備考');
        const note = cells[noteAt];
        const lh = parseFloat(getComputedStyle(note).lineHeight) || 18;
        const others = cells.filter((c, i) => i !== noteAt && c.textContent.trim());
        const nameAt = head.indexOf('品名');
        return {
          pageScrolls: document.documentElement.scrollWidth > document.documentElement.clientWidth,
          tableScrolls: t.scrollWidth > t.clientWidth + 1,
          othersNowrap: others.every(c => getComputedStyle(c).whiteSpace === 'nowrap'),
          othersClipped: others.filter(c => c.scrollWidth > c.clientWidth + 1).length,
          nameWrap: getComputedStyle(cells[nameAt]).whiteSpace,
          noteWrap: getComputedStyle(note).whiteSpace,
          noteW: Math.round(note.getBoundingClientRect().width),
          noteLines: Math.round(note.getBoundingClientRect().height / lh),
        };
      });
      if (!r) { console.log('NG ' + width + 'px 表が見つかりません'); bad++; continue; }

      const say = (ok, msg) => { console.log((ok ? '  OK ' : '  NG ') + width + 'px ' + msg); if (!ok) bad++; };
      say(!r.pageScrolls, 'ページは横へ揺れない');
      say(r.othersNowrap && r.othersClipped === 0, '備考以外は1行（品名は ' + r.nameWrap + '）');
      say(r.noteWrap === 'normal', '備考は折り返す（' + r.noteWrap + '・' + r.noteLines + '行）');
      // ⚠ 潰れていないこと。下限（14em≒224px）を割ったら、表がここだけ細らせている。
      say(r.noteW >= 200, '備考が潰れていない（' + r.noteW + 'px）');
      if (width < 1280) say(r.tableScrolls, '表が自分の中で横スクロールする');
    }
    // ── 受注残表（クローム）も同じ物差しで測る（2026-09-21）──────────────
    //
    // ⚠ **本文の表とは経路が違います。** 折り返しの印を付ける
    // `validateTypedTables`（app.js）は**サーバー所有の表を意図的に飛ばす**ので
    // （クロームは殻の持ち物）、受注残表は**サーバーが自分で印を付けています**
    // （ext/subcon/backlog.go）。**片方だけ直した日にずれる**ので、ここで一緒に見ます。
    //
    // ⚠ **受注残表が無ければ飛ばします**（壊れたのか、まだ置いていないだけなのかを
    // 読む人に分かる形にする——E2E の既存の流儀）。
    const backlogPage = await findBacklogPage(page, BASE);
    if (!backlogPage) {
      console.log('  -- 受注残表を置いたページが無いので飛ばします');
    } else {
      await page.setViewportSize({ width: 1000, height: 900 });
      await page.goto(BASE + backlogPage);
      await page.waitForTimeout(400);
      const r = await page.evaluate(() => {
        const t = document.querySelector('.backlog-table');
        if (!t) return null;
        const head = Array.from(t.querySelectorAll('tr')[0].children).map((c) => c.textContent.trim());
        const row = t.querySelectorAll('tr')[1];
        if (!row) return { empty: true };
        const cells = Array.from(row.children);
        const noteAt = head.indexOf('備考');
        const others = cells.filter((c, i) => i !== noteAt);
        return {
          pageScrolls: document.documentElement.scrollWidth > document.documentElement.clientWidth,
          othersNowrap: others.every((c) => getComputedStyle(c).whiteSpace === 'nowrap'),
          noteWrap: noteAt >= 0 ? getComputedStyle(cells[noteAt]).whiteSpace : '(列なし)',
          overflowX: getComputedStyle(t).overflowX,
        };
      });
      const say = (ok, msg) => { console.log((ok ? '  OK 受注残 ' : '  NG 受注残 ') + msg); if (!ok) bad++; };
      if (!r) console.log('  -- 受注残表が描かれていないので飛ばします');
      else if (r.empty) console.log('  -- 受注残が0行なので飛ばします');
      else {
        say(r.othersNowrap, '備考以外は1行');
        say(r.noteWrap === 'normal', '備考は折り返す（' + r.noteWrap + '）');
        say(r.overflowX === 'auto' || r.overflowX === 'scroll', '表が自分の中でスクロールできる（' + r.overflowX + '）');
        say(!r.pageScrolls, 'ページは横へ揺れない');
      }
    }
  } finally {
    await page.evaluate(async (pid) => {
      await fetch('/api/delete-page?id=' + encodeURIComponent(pid), { method: 'POST' });
    }, id);
  }
  await browser.close();
  console.log(bad === 0 ? 'PASS' : 'FAIL (' + bad + ')');
  process.exit(bad === 0 ? 0 : 1);
})();

// ブロック単位の保護を**2人のブラウザで**確かめる（2026-10-12・【考察】編集モードの再考 §6「試験の約束」）。
//
// 利用者:「排他のために編集モードを設けたが、今後は編集モードの代わりに、閲覧に邪魔なものを表示しない目的で、閲覧モードを
// 設けることにします。そして、ブロック単位の保護に変更し、保護の内容は編集中の同ブロックを同時に編集したものが居たら、
// 警告してマージできるように両方表示します」「込み入ってるのでテストは十分にお願いします」。
//
// 2人目は試験用の利用者 e2e-b（管理者ではない・パスワードは走るたびにランダム・最後に無効にする——職場の本物のサーバーで
// 流しても、知られたパスワードの利用者を残さない）。ページは自分で作り（ほかの人も書ける権限にする）、最後に消す。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-block-merge.js
const { chromium } = require('playwright');
const crypto = require('crypto');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const B_USER = 'e2e-b';

const TABLE = '<table data-id="t1aa"><caption>E2E 部品表</caption><tbody>' +
  '<tr><th>品名</th><th>数量</th></tr>' +
  '<tr data-id="r1aa"><td>甲</td><td>1</td></tr>' +
  '<tr data-id="r2aa"><td>乙</td><td>2</td></tr>' +
  '</tbody></table>';
// 機械の書き込み（タグの値を置き換える）が当たるのは、このページだけ（走るたびに変わる値）。
const TAG_OLD = 'E2E-旧-' + crypto.randomBytes(4).toString('hex');
const TAG_NEW = TAG_OLD.replace('旧', '新');
const BODY = '<h1 data-id="h1aa">【E2E】ブロック単位の保護</h1>' +
  '<dl data-type="tags" data-id="tg1a"><dt>区分</dt><dd>' + TAG_OLD + '</dd></dl>' +
  '<p data-id="p1aa">一</p><p data-id="p2aa">二</p><p data-id="p3aa">三</p><p data-id="p4aa">四</p>' + TABLE;

let bad = 0;
const check = (ok, msg, detail) => {
  if (ok) console.log('✓ ' + msg);
  else { console.log('✗ ' + msg + (detail ? '（' + detail + '）' : '')); bad++; }
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function api(page, method, url, body) {
  return page.evaluate(async ([m, u, b]) => {
    const res = await fetch(u, { method: m, headers: { 'Content-Type': 'application/json' }, body: b ? JSON.stringify(b) : undefined });
    return { status: res.status, text: await res.text() };
  }, [method, url, body]);
}

async function openEdit(ctx, id, errs) {
  const p = await ctx.newPage();
  p.on('pageerror', (e) => errs.push(e.message));
  await p.goto(BASE + '/' + id + '?edit');
  await p.waitForFunction(() => document.body.hasAttribute('edit-mode') &&
    document.querySelector('#w-editor-content [data-id="p1aa"]') &&
    document.querySelector('#w-editor-content [data-id="p1aa"]').isContentEditable, null, { timeout: 15000 });
  await sleep(500);
  return p;
}
const sel = (did) => '#w-editor-content .block-content > [data-id="' + did + '"]';
// caretAtEnd は要素の末尾にキャレットを置く（押すと帯が重なって押せないことがある——帯は打つ場所の上に出る）。
async function caretAtEnd(p, selector) {
  await p.evaluate((s) => {
    const el = document.querySelector(s);
    el.scrollIntoView({ block: 'center' });
    el.focus();
    const r = document.createRange();
    r.selectNodeContents(el);
    r.collapse(false);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
  }, selector);
}
async function typeAt(p, did, text) {
  await caretAtEnd(p, sel(did));
  await p.keyboard.type(text);
}
async function typeCell(p, rowId, col, text) {
  await caretAtEnd(p, '#w-editor-content tr[data-id="' + rowId + '"] td:nth-child(' + col + ')');
  await p.keyboard.type(text);
}
const flush = (p) => p.evaluate(() => window.wcmsFlushSave());
const textOf = (p, did) => p.evaluate((s) => (document.querySelector(s) || {}).textContent || '', sel(did));
const stored = (p, id) => p.evaluate(async (pid) => (await (await fetch('/api/load?id=' + pid)).text()), id);
async function waitText(p, did, want, ms = 6000) {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    if ((await textOf(p, did)).includes(want)) return true;
    await sleep(200);
  }
  return false;
}

(async () => {
  const browser = await chromium.launch();
  const vp = { viewport: { width: 1300, height: 900 }, ignoreHTTPSErrors: true };
  const ctxA = await browser.newContext(vp);
  const ctxB = await browser.newContext(vp);
  const errs = [];
  const admin = await ctxA.newPage();
  await login(admin, BASE, 'a', 'a', { keepMode: true });

  // 2人目を用意する（無ければ作る・パスワードはランダム・有効にする）。
  const pw = crypto.randomBytes(12).toString('hex');
  await api(admin, 'POST', '/api/admin/users', { username: B_USER, password: pw, is_admin: false });
  const setpw = await api(admin, 'POST', '/api/admin/users/password', { username: B_USER, password: pw });
  await api(admin, 'POST', '/api/admin/users/disable', { username: B_USER, disabled: false });
  check(setpw.status === 200, '2人目の利用者を用意した', setpw.status + ' ' + setpw.text.slice(0, 80));

  const id = await makePage(admin, BODY);
  try {
    check(!!id, '当て先のページを作った');
    // ほかの人も書ける権限（owner・group・other とも読み書き）。
    const perms = await api(admin, 'POST', '/api/page-perms?id=' + id, { mode: '333' });
    check(perms.status === 200, 'ページをほかの人も書けるようにした', perms.status + ' ' + perms.text.slice(0, 80));

    const pb0 = await ctxB.newPage();
    await login(pb0, BASE, B_USER, pw, { keepMode: true });
    await pb0.close();

    const A = await openEdit(ctxA, id, errs);
    const B = await openEdit(ctxB, id, errs);

    // ── 1. 違うブロックを2人が書く——両方残り、相手の画面にも届く ──
    await typeAt(A, 'p1aa', '-A1');
    await flush(A);
    await typeAt(B, 'p2aa', '-B1');
    await flush(B);
    check(await waitText(B, 'p1aa', '-A1'), '1. A の書き込みが B の画面に届く（書ける形のまま）');
    check(await waitText(A, 'p2aa', '-B1'), '1. B の書き込みが A の画面に届く');
    let s = await stored(A, id);
    check(s.includes('一-A1') && s.includes('二-B1'), '1. 正本に両方が残る', s.replace(/\s+/g, ' ').slice(0, 200));
    check(!(await B.$('.w-conflict')) && !(await A.$('.w-conflict')), '1. 違うブロックなのでぶつからない');

    // ── 2. 同じブロックを書いている最中に相手が保存——書いている間は枠を出さず、去って送ったときに並べる（相手の版が上） ──
    //    2026-10-12 昼から（利用者:「一文字打っただけで競合を検出しマージしろと言われても困ります」——早めの知らせは黄色の背景）。
    //    それまでは相手の保存が届いた時点でその場に枠を出していた。
    await typeAt(B, 'p3aa', '-B2');          // B は書きかけ（まだ保存していない）
    await typeAt(A, 'p3aa', '-A2');
    await flush(A);                          // A が先に保存 → B へ知らせ → B が受け取りに行く（書きかけは送らない）
    await sleep(2500);
    check(!(await B.$('.w-conflict')) && (await textOf(B, 'p3aa')).includes('三-B2'), '2. 書いている間は枠を出さない（書きかけはそのまま）');
    await flush(B);                          // B が去って送る → ぶつかる
    let panel = await B.waitForSelector('.w-conflict', { timeout: 6000 }).catch(() => null);
    check(!!panel, '2. 書いていたブロックを去って送ると枠が出る');
    if (panel) {
      const pt = await panel.textContent();
      check(pt.includes('三-A2') && pt.includes('a'), '2. 枠の上に相手の版と名前', pt.slice(0, 120));
      check((await textOf(B, 'p3aa')).includes('三-B2'), '2. 下の自分の版（書きかけ）はそのまま');
      await flush(B); await sleep(1500); // もう一度送っても、枠が出ているあいだは自分の版を黙って送らない
      s = await stored(A, id);
      check(s.includes('三-A2') && !s.includes('三-B2'), '2. 決めるまでは正本は相手の版のまま（自動保存が自分の版を送らない）', s.replace(/\s+/g, ' ').slice(0, 200));
      await B.click('.w-conflict .w-conflict-done'); // これで決める
      await sleep(2500);
      s = await stored(A, id);
      check(s.includes('三-B2') && !(await B.$('.w-conflict')), '2. 「これで決める」で自分の版が入り、枠が消える', s.replace(/\s+/g, ' ').slice(0, 200));
      check(await waitText(A, 'p3aa', '三-B2'), '2. 決めた版が A の画面にも届く');
    }

    // ── 3. もう一度ぶつけて「相手の版にする」 ──
    await typeAt(B, 'p4aa', '-B3');
    await typeAt(A, 'p4aa', '-A3');
    await flush(A);
    await sleep(1500);
    await flush(B);                          // 去って送るとぶつかる
    panel = await B.waitForSelector('.w-conflict', { timeout: 6000 }).catch(() => null);
    check(!!panel, '3. ふたたび枠が出る');
    if (panel) {
      await B.click('.w-conflict .w-conflict-theirs-btn');
      await sleep(2500);
      const bt = await textOf(B, 'p4aa');
      s = await stored(A, id);
      check(bt.includes('四-A3') && !bt.includes('-B3'), '3. 「相手の版にする」で B の画面が相手の版になる', bt);
      check(s.includes('四-A3') && !s.includes('-B3'), '3. 正本は相手の版のまま', s.replace(/\s+/g, ' ').slice(0, 200));
    }

    // ── 4. 表の違うセル——黙って合わさる ──
    await typeCell(B, 'r2aa', 2, '0');      // B: 乙の数量 2 → 20（書きかけ）
    await typeCell(A, 'r1aa', 2, '0');      // A: 甲の数量 1 → 10
    await flush(A);
    await sleep(1500);
    await flush(B);
    await sleep(1500);
    s = await stored(A, id);
    check(/<td>\s*10\s*<\/td>/.test(s) && /<td>\s*20\s*<\/td>/.test(s), '4. 表の違うセルの変更が両方入る', s.replace(/\s+/g, ' ').slice(-260));
    check(!(await B.$('.w-conflict')), '4. 違うセルなので枠は出ない');


    // ── 4b. 表の同じセルを2人が違う値に——並べる ──
    await typeCell(B, 'r1aa', 1, '-B');      // B: 甲 → 甲-B（書きかけ）
    await typeCell(A, 'r1aa', 1, '-A');      // A: 甲 → 甲-A
    await flush(A);
    await sleep(1500);
    await flush(B);                          // 去って送るとぶつかる
    panel = await B.waitForSelector('.w-conflict', { timeout: 6000 }).catch(() => null);
    check(!!panel, '4b. 表の同じセルを違う値にした——枠で並べる');
    if (panel) {
      check((await panel.textContent()).includes('甲-A'), '4b. 枠の上は相手の表');
      await B.click('.w-conflict .w-conflict-done');
      await sleep(2500);
      s = await stored(A, id);
      check(s.includes('甲-B') && !s.includes('甲-A'), '4b. 「これで決める」で自分の表になる', s.replace(/\s+/g, ' ').slice(-260));
    }
    // ── 5. 書いているブロックを相手が消した——「残す」 ──
    await typeAt(B, 'p2aa', '-B5');         // B が二を書きかけ
    await A.evaluate(() => {
      const el = document.querySelector('#w-editor-content .block-content > [data-id="p2aa"]');
      (el.closest('.editor-block') || el).remove();
    });
    await flush(A);
    panel = await B.waitForSelector('.w-conflict', { timeout: 6000 }).catch(() => null);
    check(!!panel && (await panel.textContent()).includes('消しました'), '5. 書いているブロックを相手が消した——「消す」「残す」を聞く');
    if (panel) {
      await B.click('.w-conflict .w-conflict-keep');
      await sleep(2500);
      s = await stored(A, id);
      check(s.includes('二-B1-B5'), '5. 「残す」で書きかけが正本へ戻る', s.replace(/\s+/g, ' ').slice(0, 220));
      check(await waitText(A, 'p2aa', '-B5'), '5. 残したブロックが A の画面にも戻る');
    }

    // ── 6. アンドゥは自分の変更だけを戻す ──
    await typeAt(A, 'p1aa', '-U');
    await sleep(1000);                       // 区切り（打つのが止まったら）
    await flush(A);
    await typeAt(B, 'p4aa', '-B6');
    await flush(B);
    check(await waitText(A, 'p4aa', '-B6'), '6. B の書き込みが A に届いた');
    await caretAtEnd(A, sel('p1aa'));
    await A.keyboard.press('Control+z');
    await sleep(2500);
    const a1 = await textOf(A, 'p1aa'), a4 = await textOf(A, 'p4aa');
    check(!a1.includes('-U'), '6. A のアンドゥで A の書いた字が戻る', a1);
    check(a4.includes('-B6'), '6. B の書いたブロックは戻らない', a4);
    s = await stored(A, id);
    check(!s.includes('-U') && s.includes('-B6'), '6. 正本も同じ', s.replace(/\s+/g, ' ').slice(0, 220));

    // ── 7. 戻そうとしたブロックを相手が書き換えていた——黙って戻さず並べる ──
    await typeAt(A, 'p3aa', '-X');
    await sleep(1000);
    await flush(A);
    check(await waitText(B, 'p3aa', '-X'), '7. A の書き込みが B に届いた');
    await typeAt(B, 'p3aa', '-Y');
    await flush(B);
    check(await waitText(A, 'p3aa', '-Y'), '7. B の書き込みが A に届いた');
    await caretAtEnd(A, sel('p1aa'));
    await A.keyboard.press('Control+z');
    panel = await A.waitForSelector('.w-conflict', { timeout: 5000 }).catch(() => null);
    check(!!panel, '7. 相手が書き換えたブロックへのアンドゥは枠で聞く');
    if (panel) {
      check((await panel.textContent()).includes('-Y'), '7. 枠の上は相手の版（いまの中身）');
      await A.click('.w-conflict .w-conflict-theirs-btn');
      await sleep(2000);
      check((await textOf(A, 'p3aa')).includes('-Y'), '7. 「相手の版にする」で相手の書き込みが残る');
    }

    // ── 8. 版から「このブロックだけ戻す」——いちばん古い版（作ったあと A が最初に書いた分まで・同じ人の10分は1つの版に
    //       まとまる）の「三」へ、三だけ戻す。ほかの人（B）の書き込みは残る。 ──
    await A.evaluate(() => { document.getElementById('w-vh-details').open = true; });
    const look = await A.waitForSelector('.version-blocks', { timeout: 6000 }).catch(() => null);
    check(!!look, '8. 版の一覧に「ブロックごとに見る」が出る');
    if (look) {
      const looks = await A.$$('.version-blocks');
      await looks[looks.length - 1].click(); // いちばん古い版
      const dlg = await A.waitForSelector('dialog.w-confirm .w-version-block-revert', { timeout: 6000 }).catch(() => null);
      check(!!dlg, '8. 違うブロックが並ぶ');
      if (dlg) {
        const items = await A.$$('dialog.w-confirm .w-version-block');
        let done = false;
        for (const it of items) {
          const v = await it.$('.w-version-block-view');
          if (!done && v && (await v.textContent()).trim() === '三') {
            await (await it.$('.w-version-block-revert')).click();
            done = true;
          }
        }
        await A.click('dialog.w-confirm .w-confirm-bar button');
        await sleep(2500);
        s = await stored(A, id);
        check(done && /<p data-id="p3aa">三<\/p>/.test(s) && s.includes('四-A3-B6') && s.includes('二-B1-B5'),
          '8. 三だけ古い版の中身に戻り、ほかの人の書き込みは残る', s.replace(/\s+/g, ' ').slice(0, 240));
        check(await waitText(B, 'p3aa', '三') && !(await textOf(B, 'p3aa')).includes('-Y'), '8. 戻したブロックが B の画面にも届く');
      }
    }

    // ── 11. 業務のボタン（機械の書き込み）も「ほかの書き手の1人」——別のブロックを書いていれば黙って届き、同じブロックなら並べる ──
    await typeAt(A, 'p1aa', '-M');          // A は一を書きかけ
    let rn = await api(admin, 'POST', '/api/admin/tag-rename', { name: '区分', from: TAG_OLD, to: TAG_NEW, dry: false });
    check(rn.status === 200 && rn.text.includes(id), '11. 機械が同じページのタグを書き換えた', rn.status + ' ' + rn.text.slice(0, 120));
    const tagged = async (p, v) => p.evaluate(([s, v]) => (document.querySelector(s) || {}).textContent.includes(v), [sel('tg1a'), v]);
    let ok = false;
    for (let i = 0; i < 30 && !ok; i++) { ok = await tagged(A, TAG_NEW); if (!ok) await sleep(200); }
    check(ok, '11. 機械の書き込みが書ける形の A に届く');
    check((await textOf(A, 'p1aa')).includes('-M'), '11. A の書きかけ（別のブロック）は消えない');
    await flush(A);                          // 保存はブロックを去るとき（2026-10-12 昼）
    await sleep(1500);
    s = await stored(A, id);
    check(s.includes(TAG_NEW) && s.includes('-M'), '11. 正本に両方が残る', s.replace(/\s+/g, ' ').slice(0, 220));
    // B がタグの値を書いている最中に機械がそのタグを書き換える——B に枠が出る。
    await caretAtEnd(B, '#w-editor-content [data-id="tg1a"] dd');
    await B.keyboard.type('-B');
    rn = await api(admin, 'POST', '/api/admin/tag-rename', { name: '区分', from: TAG_NEW, to: TAG_NEW + '2', dry: false });
    await sleep(2500);
    check(!(await B.$('.w-conflict')), '11. 書いている間は機械の書き換えでも枠を出さない（2026-10-12 昼から——去って送るまで待つ）');
    await flush(B);                          // B が去って送る
    panel = await B.waitForSelector('.w-conflict', { timeout: 6000 }).catch(() => null);
    check(!!panel && (await panel.textContent()).includes(TAG_NEW + '2'), '11. 書いているブロックを機械が書き換えた——去って送ると枠で並べる');
    if (panel) {
      await B.click('.w-conflict .w-conflict-theirs-btn');
      await sleep(1500);
    }

    // ── 12. 自分が押すボタンは、書きかけを保存してから動く（鏡のボタンの口 postJSON） ──
    await typeAt(A, 'p4aa', '-F');
    const order = await A.evaluate(async () => {
      const seen = [];
      const orig = window.fetch;
      window.fetch = (u, o) => { seen.push(String(u).split('?')[0]); return orig(u, o); };
      try { await postJSON('/api/tag-schema', {}); } finally { window.fetch = orig; }
      return seen;
    });
    check(order[0] === '/api/sync' && order.includes('/api/tag-schema'), '12. ボタンの要求より先に書きかけを保存する', order.join(' → '));
    s = await stored(A, id);
    check(s.includes('-F'), '12. ボタンが動く時点で書きかけは正本にある');

    // ── 9. ほかの人が開いているページを消すときは名前を出す ──
    let msg = '';
    A.once('dialog', async (d) => { msg = d.message(); await d.dismiss(); });
    await A.evaluate(() => document.getElementById('w-delete-page-btn').click());
    await sleep(1500);
    check(msg.includes('「' + B_USER + '」さんがこのページを開いています'), '9. 消す前の確かめに開いている人の名前', msg.slice(0, 100));

    // ── 10. 閲覧モードは読むだけ・開いたときの形を端末に憶える ──
    await B.evaluate(() => { const t = document.getElementById('w-mode-toggle'); t.checked = false; t.dispatchEvent(new Event('change')); });
    await B.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    check(!(await B.evaluate((s) => document.querySelector(s).isContentEditable, sel('p1aa'))), '10. 閲覧モードでは打てない');
    await B.goto(BASE + '/' + id);
    await sleep(1500);
    check(!(await B.evaluate(() => document.body.hasAttribute('edit-mode'))), '10. 閲覧モードを選ぶと、開き直しても閲覧（端末に憶える）');
    await B.evaluate(() => { const t = document.getElementById('w-mode-toggle'); t.checked = true; t.dispatchEvent(new Event('change')); });
    await B.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await B.goto(BASE + '/' + id);
    await sleep(1500);
    check(await B.evaluate(() => document.body.hasAttribute('edit-mode')), '10. 書ける形を選ぶと、開き直しても書ける形');
    const fresh = await browser.newContext(vp);
    const F = await fresh.newPage();
    await login(F, BASE, 'a', 'a', { keepMode: true });
    await F.goto(BASE + '/' + id);
    await sleep(1500);
    check(await F.evaluate(() => document.body.hasAttribute('edit-mode')), '10. 初めての端末（パソコン）は書ける形で開く');
    await fresh.close();
    const phone = await browser.newContext({ viewport: { width: 390, height: 800 }, ignoreHTTPSErrors: true, hasTouch: true, isMobile: true });
    const P = await phone.newPage();
    await login(P, BASE, 'a', 'a', { keepMode: true });
    await P.goto(BASE + '/' + id);
    await sleep(1500);
    check(!(await P.evaluate(() => document.body.hasAttribute('edit-mode'))), '10. 初めての端末（スマホ）は閲覧モードで開く');
    await phone.close();
  } finally {
    if (id) await deletePage(admin, id);
    await api(admin, 'POST', '/api/admin/users/disable', { username: B_USER, disabled: true });
  }
  check(errs.length === 0, 'JSエラーなし', errs.join(' / '));
  console.log(bad === 0 ? '\n結果: 合格' : '\n結果: ' + bad + '件の不合格');
  await browser.close();
  process.exitCode = bad === 0 ? 0 : 1;
})();

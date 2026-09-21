// 受注残表の「この表を印刷」が、**その表だけ**を刷るかを実測する（2026-09-21）。
//
// ⚠ **これは実機の失敗から生まれた番人です。** 最初の実装は「隠すものを名指しする」
// 形で、レールとフッタは消していましたが**本文の見出しや子ページ一覧が残り**、
// ユーザー報告は「**表ではなくページ全体が印刷されました**」でした。
//
// ⚠ **印刷は目で見ないと分からない、と諦めないこと。** Playwright の
// `emulateMedia({ media: 'print' })` は**紙と同じCSSを画面に当てます**ので、
// 「紙に何が乗るか」を**測れます**。測れるものを目視に委ねると、今回のように
// 確かめないまま出すことになります。
//
// 使い方: WCMS_BASE=https://localhost:8443 node probe-print-backlog.js
//         （受注残表を置いたページを WCMS_PAGE で指す。既定は受注箱を探す）
const { chromium } = require('playwright');

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();

  await page.goto(BASE + '/login');
  await page.fill('#username', 'a');
  await page.fill('#password', 'a');
  await page.click('button[type=submit]');
  await page.waitForLoadState('networkidle');

  // ⚠ **当て先を焼き込みません**（2026-09-16 の決定）——受注残表を持つページを探します。
  // ⚠ **先頭のスラッシュ無しでも受けます。** MSYS の bash は環境変数の先頭の `/` を
  // Windows のパスへ変換するので、`WCMS_PAGE=/000005` が
  // `C:/Program Files/Git/000005` に化けます（2026-09-21 に踏んだ。`/tmp` の罠と同族）。
  let target = process.env.WCMS_PAGE || '';
  if (target) {
    const m = /(\d{6})\s*$/.exec(target);
    target = m ? '/' + m[1] : '';
  }
  if (!target) {
    const children = await page.evaluate(async () => {
    const r = await fetch('/api/children?parent_id=000000', { credentials: 'same-origin' });
      const d = await r.json();
      // ⚠ **応答は配列で、鍵は大文字の `ID`**（`{children:[...]}` ではありません）。
      return Array.isArray(d) ? d.map((c) => c.ID || c.id) : [];
    });
    for (const id of children) {
      if (!id) continue;
      await page.goto(BASE + '/' + id);
      if (await page.locator('.backlog-sheet').count() > 0) { target = '/' + id; break; }
    }
  }
  if (!target) {
    console.log('飛ばします: 受注残表を置いたページが見つかりません（まだ置いていないだけかもしれません）');
    await browser.close();
    return;
  }

  await page.goto(BASE + target);
  await page.waitForTimeout(400);
  const sheets = await page.locator('.backlog-sheet').count();
  if (sheets === 0) {
    console.log('飛ばします: ' + target + ' に受注残表がありません');
    await browser.close();
    return;
  }
  console.log('当て先: ' + target + '（表 ' + sheets + ' 枚）');

  // ⚠ **`window.print()` は試験では開けません**（ダイアログで止まる）。
  // ボタンが行うのと**同じ下ごしらえ**だけを起こして、紙の見え方を測ります。
  await page.evaluate(() => {
    const sheet = document.querySelector('.backlog-sheet');
    let area = document.getElementById('w-print-area');
    if (!area) { area = document.createElement('div'); area.id = 'w-print-area'; document.body.appendChild(area); }
    area.textContent = '';
    const copy = sheet.cloneNode(true);
    copy.querySelectorAll('.backlog-print').forEach((b) => b.remove());
    area.appendChild(copy);
    document.body.classList.add('w-printing-sheet');
  });
  await page.emulateMedia({ media: 'print' });
  await page.waitForTimeout(200);

  const r = await page.evaluate(() => {
    const visible = (el) => {
      const s = getComputedStyle(el);
      if (s.display === 'none' || s.visibility === 'hidden') return false;
      const b = el.getBoundingClientRect();
      return b.width > 0 && b.height > 0;
    };
    const area = document.getElementById('w-print-area');
    // 紙に乗っている文字（器の中／外）を数える。
    const outsideText = [];
    document.querySelectorAll('body > *').forEach((el) => {
      if (el.id === 'w-print-area' || el.tagName === 'SCRIPT') return;
      if (visible(el)) outsideText.push(el.tagName + (el.className ? '.' + String(el.className).split(' ')[0] : ''));
    });
    // ⚠ **紙に出る列を数えます**（2026-09-21 ユーザー:「品番、品名、残、状態、備考
    // だけで良いです」「**顧客名と納期が書かれた見出しも印刷に入れて**欲しい」）。
    const t = area ? area.querySelector('.backlog-table') : null;
    const headOnPaper = t
      ? Array.from(t.querySelectorAll('tr')[0].children).filter(visible).map((c) => c.textContent.trim())
      : [];
    const firstRowCells = t && t.querySelectorAll('tr')[1]
      ? Array.from(t.querySelectorAll('tr')[1].children).filter(visible).length : 0;
    const title = area ? area.querySelector('.backlog-title') : null;
    return {
      headOnPaper,
      firstRowCells,
      titleOnPaper: title ? visible(title) : false,
      titleText: title ? title.textContent.trim() : '',
      areaVisible: area ? visible(area) : false,
      tablesInArea: area ? area.querySelectorAll('table').length : 0,
      printButtonsOnPaper: document.querySelectorAll('.backlog-print').length
        ? Array.from(document.querySelectorAll('.backlog-print')).filter(visible).length : 0,
      outside: outsideText,
    };
  });

  let bad = 0;
  if (!r.areaVisible) { console.log('✗ 印刷の器が紙に出ていません'); bad++; }
  else console.log('✓ 印刷の器が紙に出ています');

  if (r.tablesInArea !== 1) { console.log('✗ 器の中の表が ' + r.tablesInArea + ' 枚です（1枚を期待）'); bad++; }
  else console.log('✓ 器の中は表1枚');

  if (r.outside.length) {
    console.log('✗ 器の外が紙に残っています: ' + r.outside.join(', '));
    bad++;
  } else console.log('✓ 器の外は紙に出ていません（見出し・子ページ一覧・レール・フッタとも）');

  if (r.printButtonsOnPaper) { console.log('✗ 印刷ボタンが紙に出ています'); bad++; }
  else console.log('✓ 印刷ボタンは紙に出ていません');

  // ⚠ **見出し（顧客名と納期）は紙に出す。** 誰の何の納期ぶんの紙か分からないと、
  // 渡す相手を間違えます——**1枚が1回の納品の単位**なので、ここは落とせません。
  if (!r.titleOnPaper) { console.log('✗ 顧客名と納期の見出しが紙に出ていません'); bad++; }
  else console.log('✓ 見出しが紙に出ています: ' + r.titleText);

  // ⚠ **`弊社品番` を足しました**（2026-09-21 ユーザー:「**品番はあいまいさがある
  //    うえ、顧客の番号であるため、こちらで修正できません。そこで弊社品番を併記して
  //    あいまいさなく作業できるようにします**」）。
  // ⚠ **これは作業の紙です**——手元であいまいなく引ける番号が要ります。
  const WANT = ['弊社品番', '品番', '品名', '残', '状態', '備考'];
  const same = r.headOnPaper.length === WANT.length && r.headOnPaper.every((h, i) => h === WANT[i]);
  if (!same) { console.log('✗ 紙の列が ' + JSON.stringify(r.headOnPaper) + ' です（' + JSON.stringify(WANT) + ' を期待）'); bad++; }
  else console.log('✓ 紙の列は' + WANT.length + 'つ: ' + r.headOnPaper.join('・'));

  // ⚠ **見出しと値の数が揃っていること。** 片方にだけ印を付けると、紙で1つずつ
  // ずれます——列が消えるのは値の側だけなので、いちばん気づきにくい壊れ方です。
  if (r.firstRowCells && r.firstRowCells !== r.headOnPaper.length) {
    console.log('✗ 紙で見出し ' + r.headOnPaper.length + ' 列に対し値が ' + r.firstRowCells + ' 列です（1つずつずれます）');
    bad++;
  } else if (r.firstRowCells) console.log('✓ 見出しと値の数が揃っています');

  // 後片付けが効くかも見る（⚠ 印が残ると、次の Ctrl+P で1枚しか刷れません）。
  await page.emulateMedia({ media: 'screen' });
  await page.evaluate(() => {
    document.body.classList.remove('w-printing-sheet');
    const a = document.getElementById('w-print-area');
    if (a) a.textContent = '';
  });
  const after = await page.evaluate(() => ({
    cls: document.body.classList.contains('w-printing-sheet'),
    sheetsVisible: Array.from(document.querySelectorAll('.backlog-sheet'))
      .filter((el) => getComputedStyle(el).display !== 'none').length,
  }));
  if (after.cls || after.sheetsVisible === 0) { console.log('✗ 後片付けのあと画面が戻っていません'); bad++; }
  else console.log('✓ 画面は元どおり（表 ' + after.sheetsVisible + ' 枚が見えています）');

  console.log(bad === 0 ? '\n結果: 合格' : '\n結果: ' + bad + '件の不合格');
  await browser.close();
  process.exitCode = bad === 0 ? 0 : 1;
})();

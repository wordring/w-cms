// 添付を「ほかのページ」に出す一連（2026-09-14）
//
// ユーザー:「PDFがほかの部品の一部で、部品ページを作らず、ほかのページに
// 埋め込む場合はどうしますか？」
//
// 確かめるのは8つです（2026-09-15 に配線を属性へ移し、同日のコードレビューで4つ足した）:
//   ① 添付の隣に「🔗 参照」が出て、`ページID-添付ID` を写せる
//   ② 📄 ファイル表示 は列を持たず、`section[data-ref]` がエディタの語彙に載っている
//   ③ 無関係なページに、属性1つで貼っても開く
//   ④ 編集モードに配線の札が出て、押すと欄が開く
//   ⑤ スラッシュメニューから挿したものも配線できる（見出し形で挿さらない）
//   ⑥ 欄は札の直下に開き、貼り替えたら古い枠が消える
//   ⑦ 見出し形（`<h2>ファイル表示</h2>`）にも札が出る
//   ⑧ 描けない形式は開く口（`.file-view-plain`）で出る
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const lib = require('./lib');
// **当て先は走るときに探します**（2026-09-16）——ページIDも添付IDもデータを
// 入れ直すたびに変わるため（詳しくは lib.js の冒頭）。環境変数を渡せば探索を飛ばします。
let HOST = process.env.WCMS_HOST_PAGE || '';
let ATTACH = process.env.WCMS_ATTACH || '';
let NONVIEW = process.env.WCMS_NONVIEW || '';   // 同じページの .eml（描けない形式）
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({
    viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true,
    permissions: ['clipboard-read', 'clipboard-write'],
  });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await lib.login(page, BASE);
  if (!HOST || !ATTACH) {
    const rec = await lib.findRecordWithPDF(page);
    HOST = HOST || rec.pageID; ATTACH = ATTACH || rec.attachID;
  }
  if (HOST && !NONVIEW) {
    // 描けない形式は同じページの受信原本（.eml）で見ます。取り込みが必ず1つ置くので、
    // **通信記録なら必ず在ります**。無ければその項目だけ飛ばします。
    const body = await lib.bodyOf(page, HOST);
    const m = body.match(/href="\/\d{6}\/([a-z0-9]+)\.eml"/);
    NONVIEW = m ? m[1] : '';
  }
  ok(!!HOST && !!ATTACH, '当て先を見つけた（PDFを持つ通信記録）', HOST + '-' + ATTACH);
  if (!HOST || !ATTACH) { console.log('通信箱にPDF付きの記録がありません'); await browser.close(); process.exit(1); }

  // ① 添付の隣の「🔗 参照」
  await page.goto(BASE + '/' + HOST);
  await page.waitForTimeout(2000);
  const hasCopy = await page.evaluate(() =>
    document.querySelectorAll('#w-editor-content .attach-copyref').length);
  ok(hasCopy > 0, '添付の隣に「🔗 参照」が出る', hasCopy + '個');
  let copied = '';
  if (hasCopy) {
    await page.click('#w-editor-content .attach-copyref');
    await page.waitForTimeout(500);
    copied = await page.evaluate(() => navigator.clipboard.readText());
  }
  ok(copied === HOST + '-' + ATTACH, '参照が写せた', copied);

  // ② 📄 ファイル表示 がレジストリに在り、**配線の属性が語彙に載っているか**。
  //    列は持ちません（2026-09-15 に配線を属性へ移したので、中に書くものが無い）。
  //    載っていないとシリアライザが data-ref を落とし、図面が黙って消えます。
  const skeleton = await page.evaluate(async () => {
    const d = await (await fetch('/api/tag-schema')).json();
    const def = (d.vocab || []).find(v => v.type === 'file-view');
    return {
      def: def ? { name: def.display_name, cols: (def.columns || []).length } : null,
      sectionAttrs: (d.elements && d.elements.section) || [],
    };
  });
  ok(!!skeleton.def, '📄 ファイル表示 がレジストリに在る', skeleton.def ? skeleton.def.name : '');
  ok(skeleton.def && skeleton.def.cols === 0, '列は持たない（配線は属性）',
     skeleton.def ? String(skeleton.def.cols) : '');
  ok(skeleton.sectionAttrs.indexOf('data-ref') >= 0,
     'section[data-ref] がエディタの語彙に載っている', skeleton.sectionAttrs.join(','));

  // ③ 無関係なページへ、宣言していないタグ名で貼る
  const made = await page.evaluate(async () => {
    const res = await fetch('/api/new-page', {
      method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'parent=000000',
    });
    return res.url;
  });
  const id = (made.match(/\/(\d{6})/) || [])[1];
  ok(!!id, '無関係なページを作れた', id || made);
  if (!id) { await browser.close(); process.exit(1); }

  try {
    const saved = await page.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      // **配線は属性1つ**——どのページでも、部品ページでなくても開きます。
      const body = '<h1>装置まるごと</h1><p>子部品の図面をここに出します。</p>'
        + '<section data-type="file-view" data-ref="' + arg.ref + '"></section>';
      const res = await fetch('/api/save', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: body, token: lj.token || '' }),
      });
      return res.status;
    }, { id, ref: HOST + '-' + ATTACH });
    ok(saved === 200, '貼ったページを保存できた', String(saved));

    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(2200);
    const r = await page.evaluate(() => {
      const w = document.querySelector('#w-editor-content .file-view');
      return {
        shown: !!w,
        head: w ? (w.querySelector('.file-view-head') || {}).textContent : '',
        src: w ? (w.querySelector('embed') || {}).getAttribute('src') : '',
      };
    });
    ok(r.shown, '部品ページでなくてもPDFが開く');
    ok((r.src || '').indexOf('/' + HOST + '/' + ATTACH + '.pdf') === 0, 'URLが参照から導かれている', r.src);

    // ④ **編集モードで配線を触れること。** 空の section はクリックもキャレット移動も
    //    できないので、札と欄が無いと人は参照を設定できません（属性へ移した代償）。
    // ⚠ **先にロックを外します。** 上の保存で `/api/lock` を通しているので、
    // そのままだと編集モードへ入れません——別の口から握ったロックは同じ利用者でも
    // `/api/lock` で取り直せず（`ok:false, same_user:true`）、モードが閲覧のまま
    // 静かに戻ります。**札が出ないのではなく、編集モードに入っていない**という
    // 落ち方をします（実測で一度これに騙されました）。
    await page.evaluate(async (i) => {
      await fetch('/api/lock/force?id=' + i, { method: 'POST' });
    }, id);
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    // 切り替えはロック取得（ネットワーク）を挟むので、札の描き直しまで待つ。
    await page.waitForTimeout(2500);
    const wired = await page.evaluate(() => {
      const bar = document.querySelector('#w-editor-content .fv-wire');
      if (bar) bar.click();
      const pop = document.getElementById('w-fv-popover');
      return {
        bar: bar ? bar.textContent : '',
        open: !!(pop && pop.classList.contains('active')),
        value: pop ? document.getElementById('w-fv-ref').value : '',
      };
    });
    ok(/ファイル表示/.test(wired.bar), '編集モードに配線の札が出る', wired.bar);
    ok(wired.open, '札を押すとプロパティ欄が開く');
    ok(wired.value === HOST + '-' + ATTACH, '欄に今の配線が入っている', wired.value);

    // ⑤ **スラッシュメニューから挿したものも配線できること。**
    //    2026-09-15 にここが抜けていました——`usesHeadingForm` の例外に入れ忘れて
    //    「見出し形」（`<section><h2>ファイル表示</h2>`・**data-type 無し**）で挿さり、
    //    札の絞り込み（`section[data-type="file-view"]`）から外れていました。
    //    鏡は機能見出しでも立つので「参照がありません」とだけ出て、**その欄を開く
    //    手段が画面に無い**という気づきにくい壊れ方をします。
    await page.evaluate(() => {
      const pop = document.getElementById('w-fv-popover');
      if (pop) pop.classList.remove('active');
    });
    // ⚠ **本物の段落から始めること。** メニューの対象は `.editor-block` 単位
    // （`currentSlashBlock = targetElement.closest('.editor-block')`）なので、
    // 手で `<p>` を足してキャレットを置いても `null` になり、選んでも何も挿さりません。
    await page.click('#w-editor-content p');
    await page.keyboard.press('End');
    await page.keyboard.press('Enter');
    await page.waitForTimeout(300);
    await page.keyboard.type('/');
    await page.waitForTimeout(700);
    const menuOpen = await page.evaluate(() =>
      !!document.querySelector('#w-slash-menu.active'));
    ok(menuOpen, 'スラッシュメニューが開く');
    const item = await page.$('#w-slash-menu .slash-menu-item[data-type="vocab:file-view"]');
    ok(!!item, 'スラッシュメニューに 📄 ファイル表示 が出る');
    if (item && menuOpen) {
      await item.click();
      await page.waitForTimeout(900);
      const inserted = await page.evaluate(() => {
        const all = document.querySelectorAll('#w-editor-content section[data-type="file-view"]');
        // ⚠ 挿さるのは**押した段落の直後**で、末尾ではありません（既にある枠より前）。
        // 新しいほうは「まだ配線が無い」で見分けます。
        const last = Array.from(all).find(s => !s.getAttribute('data-ref'));
        return {
          count: all.length,
          bars: document.querySelectorAll('#w-editor-content .fv-wire').length,
          // 見出し形で挿さっていないこと（`<h2>ファイル表示</h2>` が本文に残らない）。
          heading: !!document.querySelector('#w-editor-content section:not([data-type]) > h2'),
          barText: last ? (last.querySelector('.fv-wire') || {}).textContent : '',
        };
      });
      ok(inserted.count === 2, '挿したものに data-type が付く', String(inserted.count));
      ok(!inserted.heading, '見出し形（<h2>ファイル表示</h2>）では挿さらない');
      ok(inserted.bars === 2, '挿した直後から配線の札が出る', String(inserted.bars));
      ok(/参照を設定/.test(inserted.barText || ''), '未設定と分かる札が出る', inserted.barText);
    }

    // ⑥ **貼り替え。** 欄は札の直下に開き（section の下端ではない）、貼り替えたら
    //    古い枠は消える（2026-09-15 コードレビュー #3・#4）。`yc0x.eml` は描けない形式。
    const re = await page.evaluate((next) => {
      const sec = Array.from(document.querySelectorAll('#w-editor-content section[data-type="file-view"]'))
        .find(s => s.getAttribute('data-ref'));
      const bar = sec.querySelector('.fv-wire');
      bar.click();
      const pop = document.getElementById('w-fv-popover');
      const pr = pop.getBoundingClientRect(), br = bar.getBoundingClientRect(), sr = sec.getBoundingClientRect();
      const before = !!sec.querySelector('.file-view');
      const input = document.getElementById('w-fv-ref');
      input.value = next;
      input.dispatchEvent(new Event('input', { bubbles: true }));
      return {
        gap: Math.round(pr.top - br.bottom), secH: Math.round(sr.height),
        before, after: !!sec.querySelector('.file-view'),
        ref: sec.getAttribute('data-ref'), bar: bar.textContent,
      };
    }, HOST + '-' + NONVIEW);
    ok(re.gap >= 0 && re.gap < 40, '欄は札の直下に開く（section の下端ではない）', 'gap=' + re.gap + 'px / section=' + re.secH + 'px');
    ok(re.before && !re.after, '貼り替えたら古い枠が消える');
    ok(re.ref === HOST + '-' + NONVIEW, '配線が新しい値になる', re.ref);

    // ⑦ **見出し形にも札が出る。** `<section><h2>ファイル表示</h2>` と人が打ったものも
    //    サーバーは file-view と解釈する（`vocabTypeOf`）ので、札が無いと「欄へ貼って」と
    //    出るのに欄を開けない（コードレビュー #2）。
    const hf = await page.evaluate(async () => {
      const ed = document.getElementById('w-editor-content');
      const sec = document.createElement('section');
      const h = document.createElement('h2'); h.textContent = 'ファイル表示';
      sec.appendChild(h); ed.appendChild(sec);
      await new Promise(r => setTimeout(r, 600));
      return { bar: !!sec.querySelector(':scope > .fv-wire'), dataType: sec.getAttribute('data-type') };
    });
    ok(hf.bar, '見出し形（data-type なし）にも配線の札が出る');
    ok(hf.dataType === null, '見出し形のまま（属性を勝手に足さない）');

    // ⑧ **描けない形式は開く口。** 閲覧へ戻して開き直すと、⑥で貼り替えた `.eml` が
    //    `.file-view-plain`（リンク）で出る——「枠は次に開いたとき」の約束の確認。
    //    ⚠ **貼り替えの直後（1.5秒以内）に閲覧へ戻します**——自動保存のデバウンスが残った
    //    まま退出すると、その分が消えていました（2026-09-15 に実測。退出時に流すよう直した）。
    //    ここが落ちたら、まず「保存後の本文」に新しい配線が入っているかを見ること。
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForTimeout(2000);
    const persisted = await page.evaluate(async (a) =>
      (await (await fetch('/api/load?id=' + a.id)).text()).indexOf('data-ref="' + a.ref + '"') >= 0,
      { id, ref: HOST + '-' + NONVIEW }).catch(() => false);
    ok(persisted, '退出の直前の貼り替えが保存されている（デバウンス待ちを流す）');
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content .file-view-plain', { timeout: 8000 }).catch(() => {});
    const plain = await page.evaluate((h) => {
      const p = document.querySelector('#w-editor-content .file-view-plain');
      const a = p && p.querySelector('.file-view-head a');
      return { shown: !!p, href: a ? a.getAttribute('href') : '', tall: p ? p.getBoundingClientRect().height : 0 };
    }, HOST);
    ok(plain.shown, '描けない形式（.eml）は開く口が出る');
    ok(plain.href === '/' + HOST + '/' + NONVIEW + '.eml', 'その口は添付そのものを指す', plain.href);
    ok(plain.tall > 0 && plain.tall < 200, '口は枠の高さ（70vh）を取らない', Math.round(plain.tall) + 'px');

    ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
  } finally {
    const del = await page.evaluate(async (i) => {
      await fetch('/api/lock/force?id=' + i, { method: 'POST' });
      const res = await fetch('/api/delete-page?id=' + encodeURIComponent(i), { method: 'POST' });
      return res.status;
    }, id);
    console.log('    後始末: 削除 ' + del + '（' + id + '）');
  }

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();

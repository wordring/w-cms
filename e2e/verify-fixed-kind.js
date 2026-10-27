// 作るときに決めるタグ（書類の種類など・internal/cms/fixed_tag.go・2026-10-27）を、管理者でない人の画面から確かめる。
//
// 利用者:「パージの新規作成時は書類の種類を指定できるように出来ますか？」→（案を出して）
// 「案の1〜3（作るときに種類を選ぶ・作ったあとは管理者だけが替えられる・ボタンから作る書類は人のメニューに出さない）で作ってください」
//
//   ① 子ページを作るメニューに、作るときに決める語の選択（人が選べる値だけ）が出る
//   ② 選んで「空のページ」を押すと、その値のタグを持つページができる
//   ③ 人の選べない値は、API で頼んでも断られる（403）
//   ④ できたページでその値を書き換えて保存すると、元の値に戻り、知らせが出る
//   ⑤ メニューのテンプレートに、人の選べない値のテンプレートが無い
//
// 語の名前と値は、設定の語彙（/api/tag-schema の fixed・creatable）から取る（試験に書き込まない）。
// 2人目は試験用の利用者 e2e-k（管理者ではない・パスワードは走るたびにランダム・最後に無効にする）。
// 当て先はトップ直下に自分で作るページ1枚（とその子）で、最後に消します。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-fixed-kind.js
const crypto = require('crypto');
const { chromium } = require('playwright');
const { login, makePage, deleteTree, saveNow } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const K_USER = 'e2e-k';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const api = (page, method, url, body) => page.evaluate(async ([m, u, b]) => {
  const res = await fetch(u, { method: m, headers: { 'Content-Type': 'application/json' }, body: b ? JSON.stringify(b) : undefined });
  return { status: res.status, text: await res.text() };
}, [method, url, body]);

const tagValuesOf = (page, id, name) => page.evaluate(async ([pid, n]) => {
  const html = await (await fetch('/api/load?id=' + pid)).text();
  const doc = new DOMParser().parseFromString(html, 'text/html');
  const out = [];
  doc.querySelectorAll('dl[data-type="tags"] > dt').forEach((dt) => {
    if (dt.textContent.trim() === n && dt.nextElementSibling) out.push(dt.nextElementSibling.textContent.trim());
  });
  return out;
}, [id, name]);

(async () => {
  const browser = await chromium.launch();
  const adminCtx = await browser.newContext({ ignoreHTTPSErrors: true });
  const admin = await adminCtx.newPage();
  await login(admin, BASE);

  const schema = await admin.evaluate(async () => (await fetch('/api/tag-schema')).json());
  const fixed = Object.entries(schema.vocabulary || {}).find(([, w]) => w.fixed && (w.creatable || []).length);
  if (!fixed) {
    console.log('- 飛ばす: 設定に作るときに決める語（fixed と creatable）がありません');
    await browser.close();
    return;
  }
  const [NAME, WORD] = fixed;
  const OK_VALUE = WORD.creatable[0];
  const NG_VALUE = (WORD.values || []).find((v) => !WORD.creatable.includes(v));

  const parent = await makePage(admin, '<h1>【E2E】種類を選ぶ</h1><p>子ページを作る試験</p>');
  const pw = crypto.randomBytes(12).toString('hex');
  let kCtx;
  try {
    // 管理者でない人も書ける親にする。
    await api(admin, 'POST', '/api/page-perms?id=' + parent, { mode: '333', group: '' });
    await api(admin, 'POST', '/api/admin/users', { username: K_USER, password: pw, is_admin: false });
    await api(admin, 'POST', '/api/admin/users/password', { username: K_USER, password: pw });
    await api(admin, 'POST', '/api/admin/users/disable', { username: K_USER, disabled: false });

    kCtx = await browser.newContext({ ignoreHTTPSErrors: true });
    const k = await kCtx.newPage();
    await login(k, BASE, K_USER, pw, { keepMode: true });
    await k.goto(BASE + '/' + parent);
    await k.waitForSelector('#w-create-subpage-btn', { state: 'visible', timeout: 15000 });

    // ① メニューの選択
    await k.click('#w-create-subpage-btn');
    await k.waitForSelector('#w-template-menu.active', { timeout: 10000 });
    const menu = await k.evaluate((n) => {
      const m = document.getElementById('w-template-menu');
      const sel = m.querySelector('select[data-fixed-name="' + n + '"]');
      return {
        options: sel ? [...sel.options].map((o) => o.value) : null,
        buttons: [...m.querySelectorAll('button')].map((b) => b.textContent.trim()),
      };
    }, NAME);
    check('① メニューに「' + NAME + '」の選択が出る', !!menu.options, JSON.stringify(menu.options));
    check('① 選べるのは人が選べる値だけ（なしも選べる）',
      !!menu.options && menu.options[0] === '' && menu.options.slice(1).join(',') === WORD.creatable.join(','));

    // ② 選んで空のページを作る
    await k.selectOption('#w-template-menu select[data-fixed-name="' + NAME + '"]', OK_VALUE);
    const [child] = await Promise.all([
      kCtx.waitForEvent('page'),
      k.click('#w-template-menu button:text-is("空のページ")'),
    ]);
    await child.waitForLoadState('networkidle');
    const childID = (child.url().match(/\/(\d{6})/) || [])[1];
    check('② 子ページができた', !!childID, child.url());
    const vals = childID ? await tagValuesOf(k, childID, NAME) : [];
    check('② 選んだ値のタグを持つ', vals.join(',') === OK_VALUE, JSON.stringify(vals));

    // ③ 人の選べない値は断る
    if (NG_VALUE) {
      const res = await k.evaluate(async ([p, n, v]) => {
        const body = new URLSearchParams({ parent: p });
        body.set('fixed.' + n, v);
        const r = await fetch('/api/new-page', { method: 'POST', body, redirect: 'manual' });
        return r.status;
      }, [parent, NAME, NG_VALUE]);
      check('③ 人の選べない値（' + NG_VALUE + '）では作れない', res === 403, 'status=' + res);
    }

    // ④ 値を書き換えて保存すると戻る
    if (childID) {
      await child.waitForSelector('#w-editor-content dl[data-type="tags"]', { timeout: 15000 });
      const sel = await child.evaluate((n) => {
        const dt = [...document.querySelectorAll('#w-editor-content dl[data-type="tags"] > dt')].find((x) => x.textContent.trim() === n);
        if (!dt || !dt.nextElementSibling) return null;
        dt.nextElementSibling.setAttribute('data-e2e', 'fixed-dd');
        return '[data-e2e="fixed-dd"]';
      }, NAME);
      check('④ 値の欄がある', !!sel);
      if (sel) {
        await child.click(sel, { clickCount: 3 });
        await child.keyboard.type((NG_VALUE || 'E2E') + 'X');
        await child.click('#w-editor-content h1');
        await saveNow(child);
        await child.waitForTimeout(500);
        const after = await tagValuesOf(k, childID, NAME);
        check('④ 保存しても元の値のまま', after.join(',') === OK_VALUE, JSON.stringify(after));
        const shown = await child.evaluate(() => document.body.innerText.includes('替えられるのは管理者だけ'));
        check('④ 戻したことを知らせる', shown);
        const onScreen = await child.evaluate(() => {
          const el = document.querySelector('#w-editor-content [data-e2e="fixed-dd"]') ||
            [...document.querySelectorAll('#w-editor-content dl[data-type="tags"] > dd')][0];
          return el ? el.textContent.trim() : '';
        });
        check('④ 画面も元の値に描き直す', onScreen === OK_VALUE, onScreen);
      }
    }

    // ⑤ メニューのテンプレートに人の選べない値のものが無い
    const bad = await k.evaluate(async ([n, creatable]) => {
      const tree = await (await fetch('/api/templates')).json();
      const leaves = [];
      const walk = (ns) => ns.forEach((x) => (x.children && x.children.length ? walk(x.children) : leaves.push(x)));
      walk(tree);
      const out = [];
      for (const l of leaves) {
        const html = await (await fetch('/api/load?id=' + l.id)).text();
        const doc = new DOMParser().parseFromString(html, 'text/html');
        doc.querySelectorAll('dl[data-type="tags"] > dt').forEach((dt) => {
          const v = dt.nextElementSibling && dt.nextElementSibling.textContent.trim();
          if (dt.textContent.trim() === n && v && !creatable.includes(v)) out.push(l.title + '（' + v + '）');
        });
      }
      return out;
    }, [NAME, WORD.creatable]);
    check('⑤ メニューのテンプレートに人の選べない値のものが無い', bad.length === 0, bad.join('・'));
  } finally {
    if (kCtx) await kCtx.close();
    await deleteTree(admin, parent);
    await api(admin, 'POST', '/api/admin/users/disable', { username: K_USER, disabled: true });
    const left = await admin.evaluate(async (id) => (await fetch('/api/load?id=' + id)).status, parent);
    check('後片付け: 作ったページを消した', left === 404 || left === 403, 'status=' + left);
    await browser.close();
  }
  console.log(fails ? '✗ ' + fails + ' 件の失敗' : '✓ 全部通りました');
  process.exit(fails ? 1 : 0);
})();

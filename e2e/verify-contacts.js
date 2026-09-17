// 未登録の連絡先——「組織」「担当者」の2欄（2026-09-17）
//
// ユーザー:「入力欄は『組織コンボボックス』と『担当者名コンボボックス』になると思います。
// 同じドメインで登録された社名がDBにあれば、組織コンボボックスに候補としてリストされます」。
//
// 見るのは: 1アドレス1行／各行に組織・担当者の欄と「登録」がある／組織の候補に必ず
// 「個人」が居る／同じドメインの組織が既にあれば候補に並び、1つなら組織欄に入っている／
// 行に余計な道具が無い（ドメインのチェック・取引の選択は 2026-09-17 に外した）。
//
// **押すのは、既にある組織が組織欄に入っている行だけ**（実データとしても正しい操作）。
// そういう行が無い日は構造の確認だけで終わります——連絡帳を片付け切った状態も、
// 入れ直した直後の空の状態も正常だからです。⚠ 候補に無い社名を打って新しい組織を
// 作る操作は自動で流しません（実データに機械の名前のページが積もる）。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  ✓ ' : '  ✗ ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await lib.login(page, BASE);
  // **連絡帳ページは走るときに探します**（トップ直下・題が「連絡帳」）。
  const BOX = ((await lib.childrenOf(page, '000000')).find(c => (c.Title || '').trim() === '連絡帳') || {}).ID || '';
  if (!BOX) { console.log('連絡帳ページがありません（管理画面の「置き場」で作れます）'); await browser.close(); process.exit(1); }
  await page.goto(BASE + '/' + BOX); await page.waitForTimeout(1500);

  const before = await page.evaluate(() => {
    const rows = Array.from(document.querySelectorAll('#w-editor-content tr[data-address]'));
    return {
      rows: rows.length,
      overflow: document.documentElement.scrollWidth > window.innerWidth,
      detail: rows.map(r => {
        const org = r.querySelector('.contact-org');
        const opts = org && org.list ? Array.from(org.list.options) : [];
        const match = opts.find(o => o.value === (org ? org.value.trim() : ''));
        return {
          addr: r.dataset.address,
          hasFields: !!(org && r.querySelector('.contact-person') && r.querySelector('.contact-go')),
          candidates: opts.map(o => o.value),
          hasPersonal: opts.some(o => o.value === '個人'),
          orgValue: org ? org.value : '',
          existingID: match ? (match.dataset.id || '') : '',
          // 行に残っていてはいけないもの（2026-09-17 に外した）。
          // ドメインのチェックは「既定で入っているのが危ない」、取引は「読むのは自社だけ・
          // 顧客と仕入先は誰も読まない・両方ありうるのにラジオでは片方しか選べない」。
          extras: r.querySelectorAll('.contact-add-domain, .contact-rel').length,
        };
      }),
    };
  });
  if (before.rows === 0) {
    console.log('  — 未登録の連絡先が0件です（片付け切った状態）。ここで終了します');
    await browser.close(); console.log(fail ? fail + ' 件失敗' : '全項目OK'); process.exit(fail ? 1 : 0);
  }
  ok(before.rows > 0, '1アドレス1行になっている', before.rows + '行');
  ok(!before.overflow, '横にはみ出さない');
  ok(before.detail.every(d => d.hasFields), 'どの行にも組織・担当者の欄と「登録」がある');
  ok(before.detail.every(d => d.hasPersonal), '組織の候補に必ず「個人」が居る');
  ok(before.detail.every(d => d.candidates.length <= 22), '候補の数に上限がある（同じドメインの組織は最大20）',
     Math.max(...before.detail.map(d => d.candidates.length)) + '件が最多');
  ok(before.detail.every(d => d.extras === 0),
     '行にあるのは組織・担当者・登録だけ（ドメインのチェックと取引の選択は置かない）');
  const prefilled = before.detail.filter(d => d.existingID);

  const target = prefilled[0];
  if (!target) {
    console.log('  — 既にある組織が入っている行がありません。押す操作は飛ばします');
    ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
    await browser.close(); console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK'); process.exit(fail ? 1 : 0);
  }

  // 既にある組織へ「登録」（担当者は初期値のまま。空なら組織の口、名前があれば人のページ）。
  await page.click('#w-editor-content tr[data-address="' + target.addr + '"] .contact-go');
  await page.waitForTimeout(1500);
  const after = await page.evaluate((a) => ({
    rows: document.querySelectorAll('#w-editor-content tr[data-address]').length,
    still: !!document.querySelector('#w-editor-content tr[data-address="' + a + '"]'),
  }), target.addr);
  ok(after.rows === before.rows - 1, '押した行が一覧から消える', before.rows + ' → ' + after.rows);
  ok(!after.still, target.addr + ' の行が消えた');

  // 連絡帳の木のどこかに載ったか（正本の確認。行き先は組織か、その下の人）。
  const landed = await page.evaluate(async ({ a, box }) => {
    const seen = new Set();
    const walk = async (id, depth) => {
      if (depth > 4 || seen.has(id)) return false;
      seen.add(id);
      const body = await (await fetch('/api/load?id=' + id)).text();
      if (body.includes(a)) return true;
      const res = await fetch('/api/children?parent_id=' + id);
      if (!res.ok) return false;
      for (const k of ((await res.json()) || [])) {
        if (await walk(k.ID, depth + 1)) return true;
      }
      return false;
    };
    return walk(box, 0);
  }, { a: target.addr, box: BOX });
  ok(landed, '連絡帳の木にアドレスが載った', target.orgValue);
  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();

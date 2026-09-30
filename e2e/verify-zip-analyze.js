// メールの ZIP の隣の「🤖 中のPDFをまとめて解析」を画面から確かめる（2026-09-30）。
//
// 利用者:「添付ファイルのPDFについて、ZIPの場合まとめて解析するオプションが欲しいです」。
//
//   ① ZIP（中にPDF 2つ）の付いたメールを通信箱へ落とす——取り込みが ZIP を展開して中身を1つずつ添付にする
//   ② ZIP の隣に「🤖 中のPDFをまとめて解析（2件）」が出る
//   ③ 押すと中のPDFを中のパスの順に1つずつ解析し、最後に1つの知らせにまとめる
//      （解析の口は差し止める——Gemini は呼ばない・ページも作らない）
//   ④ 解析済みのPDFは飛ばす（1件を解析済みに見せると「（1件）」になり、その1件だけを解析する）
//
// 当て先（メールの記録）は自分で作って最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-zip-analyze.js
const { chromium } = require('playwright');
const { login, deletePage, findMailbox, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

// ── 無圧縮ZIPを手組みする（verify-preview.js と同じ・素材をリポジトリへ置かないため） ──
const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = (c & 1) ? (0xEDB88320 ^ (c >>> 1)) : (c >>> 1);
    t[n] = c >>> 0;
  }
  return t;
})();
function crc32(buf) {
  let c = 0xFFFFFFFF;
  for (const b of buf) c = CRC_TABLE[(c ^ b) & 0xFF] ^ (c >>> 8);
  return (c ^ 0xFFFFFFFF) >>> 0;
}
function u16(n) { const b = Buffer.alloc(2); b.writeUInt16LE(n); return b; }
function u32(n) { const b = Buffer.alloc(4); b.writeUInt32LE(n >>> 0); return b; }
function buildZip(files) {
  const locals = [], centrals = [];
  let offset = 0;
  for (const f of files) {
    const name = Buffer.from(f.name), data = Buffer.from(f.data), crc = crc32(data);
    const local = Buffer.concat([
      Buffer.from('PK\x03\x04', 'binary'), u16(20), u16(0), u16(0), u16(0), u16(0x21),
      u32(crc), u32(data.length), u32(data.length), u16(name.length), u16(0), name, data]);
    centrals.push(Buffer.concat([
      Buffer.from('PK\x01\x02', 'binary'), u16(20), u16(20), u16(0), u16(0), u16(0), u16(0x21),
      u32(crc), u32(data.length), u32(data.length), u16(name.length),
      u16(0), u16(0), u16(0), u16(0), u32(0), u32(offset), name]));
    locals.push(local);
    offset += local.length;
  }
  const cd = Buffer.concat(centrals);
  const eocd = Buffer.concat([
    Buffer.from('PK\x05\x06', 'binary'), u16(0), u16(0), u16(files.length), u16(files.length),
    u32(cd.length), u32(offset), u16(0)]);
  return Buffer.concat([...locals, cd, eocd]);
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  page.on('dialog', (d) => d.accept());
  let record = '';
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    if (!box) {
      console.log('通信箱が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }

    // ① ZIP の付いたメールを通信箱へ。
    const zip = buildZip([
      { name: 'drawings/B-200.pdf', data: '%PDF-1.4 e2e B' },
      { name: 'drawings/A-100.pdf', data: '%PDF-1.4 e2e A' },
    ]).toString('base64').replace(/.{76}/g, '$&\r\n');
    const now = new Date();
    const eml = [
      'From: E2E <e2e-zip@invalid.example>',
      'To: admin@invalid.example',
      'Subject: E2E zip analyze',
      'Date: ' + now.toUTCString().replace('GMT', '+0000'),
      'Message-ID: <e2e-zip-' + Date.now() + '@invalid.example>',
      'MIME-Version: 1.0',
      'Content-Type: multipart/mixed; boundary="b1"',
      '',
      '--b1',
      'Content-Type: text/plain; charset=UTF-8',
      '',
      'E2E の図面です。',
      '--b1',
      'Content-Type: application/zip; name="drawings.zip"',
      'Content-Disposition: attachment; filename="drawings.zip"',
      'Content-Transfer-Encoding: base64',
      '',
      zip,
      '--b1--',
      '',
    ].join('\r\n');
    const up = await page.evaluate(async (a) => {
      const fd = new FormData();
      fd.append('page_id', a.box);
      fd.append('file', new Blob([a.eml], { type: 'message/rfc822' }), 'e2e-zip.eml');
      const res = await fetch('/api/upload-file', { method: 'POST', body: fd });
      return res.json().catch(() => ({}));
    }, { box, eml });
    record = up.page_id || '';
    check('① ZIP の付いたメールを取り込めた', up.intake === true && !!record, JSON.stringify(up).slice(0, 120));

    // ② ボタン
    await page.goto(BASE + '/' + record);
    const zb = page.locator('#w-editor-content .attach-analyze-zip');
    await zb.waitFor({ timeout: 8000 });
    check('② ZIP の隣に「まとめて解析（2件）」', (await zb.textContent()).includes('（2件）'), await zb.textContent());
    check('② 中のPDFにも1件ずつの「🤖 解析」', await page.locator('#w-editor-content .attach-analyze:not(.attach-analyze-zip)').count() === 2);

    // ③ 押す——解析の口は差し止める。
    const asked = [];
    await page.route('**/api/analyze-attachment', async (route) => {
      const body = JSON.parse(route.request().postData() || '{}');
      asked.push(body.file);
      const first = asked.length === 1;
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(first
        ? { success: true, doc_type: 'drawing', pages: [{ page_id: '999998', title: 'A-100' }] }
        : { success: true, doc_type: 'other', is_client_order: false }) });
    });
    // 押したあとの読み直しでページが無い（差し止めた）ので、読み直しの失敗は気にしない。
    await zb.click();
    const toast = page.locator('.toast', { hasText: 'まとめて解析しました' });
    await toast.waitFor({ timeout: 15000 });
    const told = await toast.textContent();
    const files = await page.evaluate(async (id) => {
      const d = await (await fetch('/api/analyzed?page_id=' + id)).json();
      return Object.values(d.zip_pdfs || {})[0] || [];
    }, record);
    check('③ 中のパスの順に2つとも解析した', asked.length === 2 && files.length === 2 &&
      asked[0] === files[0].file && asked[1] === files[1].file && files[0].path === 'drawings/A-100.pdf',
      JSON.stringify(asked) + ' ' + JSON.stringify(files));
    check('③ 1つの知らせにまとめる', told.includes('（2件）') && told.includes('加工製品ページ 1枚') &&
      told.includes('drawings/B-200.pdf'), told);

    // ④ 解析済みは飛ばす——A を解析済みに見せる。
    await page.unrouteAll({ behavior: 'ignoreErrors' });
    asked.length = 0;
    await page.route('**/api/analyzed?*', async (route) => {
      const res = await route.fetch();
      const d = await res.json();
      d.analyzed = { [files[0].id]: { kind: '図面', page_id: '999998', title: 'A-100' } };
      await route.fulfill({ response: res, json: d });
    });
    await page.route('**/api/analyze-attachment', async (route) => {
      asked.push(JSON.parse(route.request().postData() || '{}').file);
      await route.fulfill({ status: 200, contentType: 'application/json',
        body: JSON.stringify({ success: true, doc_type: 'other', is_client_order: false }) });
    });
    await page.goto(BASE + '/' + record);
    await zb.waitFor({ timeout: 8000 });
    check('④ 解析済みを除いて「（1件）」', (await zb.textContent()).includes('（1件）'), await zb.textContent());
    await zb.click();
    await page.locator('.toast', { hasText: 'まとめて解析しました（1件）' }).waitFor({ timeout: 15000 });
    check('④ 解析済みでない1件だけを解析した', asked.length === 1 && asked[0] === files[1].file, JSON.stringify(asked));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await page.unrouteAll({ behavior: 'ignoreErrors' }).catch(() => {});
    if (record) {
      for (const c of await childrenOf(page, record).catch(() => [])) await deletePage(page, c.ID).catch(() => {});
      await deletePage(page, record).catch(() => {});
    }
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();

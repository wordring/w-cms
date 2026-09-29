// 加工製品の木を段から「加工製品」の箱へ移す（1回だけの道具・2026-09-29）。
//
// 利用者:「試作フォルダについてですが、フォルダで分けるのをやめて、試作のタグをつけるように変更したい」
// 「〈取引先〉／加工製品／装置名称／品目にしたい」。
//
//   取引先／社名／現行／装置名称／…   →   取引先／社名／加工製品／装置名称／…
//   取引先／社名／旧型／装置名称／…   →   同上（品目に「区分：旧型」を人が付ける——一覧に出す）
//   取引先／社名／試作／装置名称／…   →   同上（品目に「区分：試作」を人が付ける）
//
//   node tools/migrate-product-tree.js          … 何をするかを並べるだけ（既定）
//   node tools/migrate-product-tree.js --run    … 実際に移す
//
// ⚠ **編集ロックを奪いません**——開いている人がいるページは飛ばして知らせます（もう一度流せば続きから）。
// ⚠ **同じ題の装置が「加工製品」の下に既にあれば動かしません**（中身を混ぜるかは人が決める）。
// ⚠ 空になった段のページは、**本文が題だけなら**ゴミ箱へ移します（data/trash・戻せる）。何か書いてあれば残して知らせます。
// 「加工製品」の箱はサーバーが作ります（/api/product-folder・テンプレート「取引先の加工製品」から）。
//
// 当て先は WCMS_BASE（既定 https://localhost:8443）。サーバーを動かしたまま、リポジトリの根で流します。
// ⚠ 両方の環境で流し終えたら、この道具は消すこと（作業引き継ぎ）。
process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0'; // 自己署名の :8443 に繋ぐため（ローカル専用）
const fs = require('fs');
const path = require('path');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const MASTER = path.join(process.cwd(), 'data', 'master');
const STAGES = ['現行', '旧型', '試作']; // 2026-09-29 まで設定 machine_stages にあった段
const BOX = '加工製品';
const RUN = process.argv.includes('--run');
let cookie = '';

async function req(method, p, { body, headers = {} } = {}) {
  const res = await fetch(BASE + p, { method, body, redirect: 'manual', headers: { Origin: BASE, Cookie: cookie, ...headers } });
  for (const c of (res.headers.getSetCookie ? res.headers.getSetCookie() : [])) {
    const kv = c.split(';')[0], name = kv.split('=')[0];
    cookie = cookie.split('; ').filter(x => x && !x.startsWith(name + '=')).concat(kv).join('; ');
  }
  return res;
}
async function children(id) {
  const r = await req('GET', '/api/children?parent_id=' + id);
  if (!r.ok) return [];
  const d = await r.json();
  return (Array.isArray(d) ? d : []).map(c => ({ id: c.ID, title: (c.Title || '').trim() }));
}
// 本文は正本のファイルから読む（/api/load は鏡を埋めて返すので使わない）。
const bodyOf = id => fs.readFileSync(path.join(MASTER, id.slice(0, 2), id, id + '.html'), 'utf8');
// ⚠ **奪わない**——force を使わず、取れなければ null。
async function lock(id) {
  const r = await req('POST', '/api/lock?id=' + id);
  if (r.status !== 200) return null;
  const d = await r.json().catch(() => ({}));
  return d.token || null;
}
const unlock = (id, token) => req('POST', `/api/unlock?id=${id}&token=${encodeURIComponent(token)}`);

(async () => {
  const lr = await req('POST', '/api/login', { body: 'username=a&password=a',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' } });
  if ((lr.headers.get('location') || '').includes('error')) throw new Error('ログインできません');
  const top = await children('000000');
  const partnersBox = top.find(c => c.title === '取引先');
  if (!partnersBox) { console.log('取引先のページがありません——移すものはありません'); return; }

  console.log(RUN ? '── 移します ──' : '── 何をするか（--run を付けると実際に移します）──');
  let moved = 0, todo = 0;
  const asks = [];
  for (const partner of await children(partnersBox.id)) {
    const stages = (await children(partner.id)).filter(c => STAGES.includes(c.title));
    if (!stages.length) continue;
    let boxID = (await children(partner.id)).find(c => c.title === BOX)?.id || '';
    if (!boxID && RUN) {
      const r = await req('POST', '/api/product-folder', { body: JSON.stringify({ customer: partner.title, machine: '' }),
        headers: { 'Content-Type': 'application/json' } });
      const d = await r.json().catch(() => ({}));
      if (!r.ok || !d.page_id) { asks.push(`${partner.title}: 「${BOX}」の箱を作れません（${d.message || r.status}）`); continue; }
      boxID = d.page_id;
      console.log(`  ${partner.title}／${BOX} を作りました（${boxID}）`);
    }
    const inBox = boxID ? await children(boxID) : [];
    for (const stage of stages) {
      for (const mach of await children(stage.id)) {
        const where = `${partner.title}／${stage.title}／${mach.title}（${mach.id}）`;
        if (inBox.some(c => c.title === mach.title)) {
          asks.push(`${where}: 「${BOX}」の下に同じ題の装置が既にあります——中身をどちらへ寄せるか決めてください`);
          continue;
        }
        todo++;
        if (STAGES.indexOf(stage.title) > 0) {
          const kids = await children(mach.id);
          asks.push(`${where}: 下の品目 ${kids.length} 枚に「区分：${stage.title}」を付けてください（${kids.map(k => k.id).join('・')}）`);
        }
        if (!RUN) { console.log(`  移す: ${where} → ${partner.title}／${BOX}`); continue; }
        const token = await lock(mach.id);
        if (!token) { asks.push(`${where}: 誰かが開いているので動かしませんでした（閉じてからもう一度）`); continue; }
        const r = await req('POST', `/api/set-parent?id=${mach.id}&parent=${boxID}&token=${encodeURIComponent(token)}`);
        await unlock(mach.id, token);
        if (r.status !== 200) { asks.push(`${where}: 動かせませんでした（${r.status} ${(await r.text()).trim()}）`); continue; }
        moved++;
        inBox.push(mach);
        console.log(`  移した: ${where} → ${partner.title}／${BOX}`);
      }
      // 空になった段のページ——本文が題だけなら消す（ゴミ箱へ）。
      if (!RUN) continue;
      if ((await children(stage.id)).length) continue;
      const body = bodyOf(stage.id).replace(/\s+/g, '');
      if (body !== `<h1>${stage.title}</h1>`) {
        asks.push(`${partner.title}／${stage.title}（${stage.id}）: 空になりましたが本文に何か書いてあるので残しました`);
        continue;
      }
      const token = await lock(stage.id);
      if (!token) { asks.push(`${partner.title}／${stage.title}（${stage.id}）: 開いている人がいるので消していません`); continue; }
      const r = await req('POST', `/api/delete-page?id=${stage.id}&token=${encodeURIComponent(token)}`);
      if (r.status !== 200) {
        await unlock(stage.id, token);
        asks.push(`${partner.title}／${stage.title}（${stage.id}）: 消せませんでした（${r.status}）`);
        continue;
      }
      console.log(`  空の段を消した（ゴミ箱へ）: ${partner.title}／${stage.title}（${stage.id}）`);
    }
  }
  console.log(RUN ? `移した装置: ${moved}` : `移す装置: ${todo}`);
  if (asks.length) console.log('\n── 人の手が要るもの ──\n' + asks.map(a => '  ' + a).join('\n'));
})().catch(e => { console.error('失敗:', e.message); process.exit(1); });

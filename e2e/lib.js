// E2E の共通ヘルパ——**当て先を焼き込まず、走るときに探す**（2026-09-16）
//
// ページIDと添付IDを定数で持っていたので、**データを入れ直すたびに E2E が落ちて
// いました**。しかも落ち方が分かりにくい:
//
//   - 2026-09-13 の入れ直し … `verify-drawing-size` が落ち、原因を「当て先がずれた」
//     と記録したが、**理由は2つあった**（もう一方は消えた class）。片方だけ直しても通らない
//   - 2026-09-16 の一掃 …… 10本のスクリプトの当て先が全部（`010153`→`000001`・
//     `010272`→`000021`）入れ替わり、添付ID（`c3p7`）にいたっては**毎回ランダム**
//
// **定数を新しい値へ書き換えるのは直したことになりません**——次の入れ直しでまた同じ
// ことが起きます。ここは「**その環境で条件に合うページを探す**」ことで、入れ直しに
// 耐える形にします。探す手掛かりは**機能の定義そのもの**です:
//
//   通信箱   = トップ直下で題が「通信箱」（`MailBoxTitle`。名前が機能・仕様）
//   通信記録 = 通信箱の子孫で `チャネル` タグを持つページ
//   解析の的 = そのうち `.pdf` の添付を持つもの（添付IDは本文のリンクから読む）
//
// 環境変数（`WCMS_MAILBOX`・`WCMS_HOST_PAGE`・`WCMS_ATTACH`）を渡せば探索を飛ばします
// ——特定のページで確かめたいときのため。
//
// ⚠ **探索は「読むだけ」です。** ページを作ったり書き換えたりしません（E2E が
// 実データを汚さないため）。作る必要があるスクリプトは `makePage` で作り、
// **最後に `deletePage` で必ず消すこと**（2026-09-23 に集約——4本が同じ20行を写していた）。

const MAILBOX_TITLE = '通信箱';
const CHANNEL_TAG = 'チャネル';

// login はログインして networkidle まで待ちます（どのスクリプトも同じ形だったので集約）。
async function login(page, base, user = 'a', pass = 'a') {
  await page.goto(base + '/login');
  await page.fill('#username', user);
  await page.fill('#password', pass);
  await page.click('button[type=submit]');
  await page.waitForLoadState('networkidle');
}

// makePage はページを1枚作り、本文を書いてIDを返します（作れなければ空文字）。
//
// **当て先を自分で作るスクリプトのため**の口です（09-21〜22 の4本が同じ20行を
// 写していたので寄せた・2026-09-23）。⚠ **作ったページは最後に `deletePage` で必ず
// 消すこと**——残すとトップ直下にゴミが積もり、次の E2E が「その題のページが在る」と
// 誤読します。⚠ **握ったロックは外して返します**——残すと削除にも入れません。
async function makePage(page, html, parent = '000000') {
  const url = await page.evaluate(async (p) => {
    const res = await fetch('/api/new-page', {
      method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'parent=' + encodeURIComponent(p),
    });
    return res.url;
  }, parent);
  const id = (url.match(/\/(\d{6})/) || [])[1];
  if (!id) return '';
  await page.evaluate(async (arg) => {
    const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
    const lj = await lr.json().catch(() => ({}));
    await fetch('/api/save', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }),
    });
    await fetch('/api/lock/force?id=' + arg.id, { method: 'POST' });
  }, { id, html });
  return id;
}

// deletePage は makePage で作ったページを消します（ロックが残っていても外してから）。
// 空のIDなら何もしません（作れなかった当て先の後片付けを、呼ぶ側で場合分けしないため）。
async function deletePage(page, id) {
  if (!id) return;
  await page.evaluate(async (pid) => {
    await fetch('/api/lock/force?id=' + pid, { method: 'POST' });
    await fetch('/api/delete-page?id=' + encodeURIComponent(pid), { method: 'POST' });
  }, id);
}

// childrenOf は子ページを `[{ID, Title}]` で返します。
async function childrenOf(page, pageID) {
  return page.evaluate(async (id) => {
    const res = await fetch('/api/children?parent_id=' + encodeURIComponent(id));
    if (!res.ok) return [];
    return res.json().catch(() => []);
  }, pageID);
}

// bodyOf はサニタイズ済みの本文HTMLを返します（`/api/load` は生のHTML）。
async function bodyOf(page, pageID) {
  return page.evaluate(async (id) => {
    const res = await fetch('/api/load?id=' + encodeURIComponent(id));
    return res.ok ? res.text() : '';
  }, pageID);
}

// findMailbox は通信箱のページIDを返します（無ければ空）。
//
// **題で探すのは仕様どおり**です——「表示されている言葉が機能を表す」（通信箱・
// テンプレート置き場・取引先が共有する形）。
async function findMailbox(page) {
  if (process.env.WCMS_MAILBOX) return process.env.WCMS_MAILBOX;
  const top = await childrenOf(page, '000000');
  const hit = top.find(c => (c.Title || '').trim() === MAILBOX_TITLE);
  return hit ? hit.ID : '';
}

// findRecordWithPDF は「`チャネル` タグと `.pdf` の添付を持つ通信記録」を1つ探し、
// `{pageID, attachID, fileName}` を返します（見つからなければ pageID が空）。
//
// 通信箱の下は「年／月／記録」の3段ですが、**深さを決め打ちしません**——手で作った
// 記録が直下に在ることもあるためです。幅優先で、見つかった時点で止めます。
async function findRecordWithPDF(page, opts = {}) {
  if (process.env.WCMS_HOST_PAGE && process.env.WCMS_ATTACH) {
    return { pageID: process.env.WCMS_HOST_PAGE, attachID: process.env.WCMS_ATTACH, fileName: '' };
  }
  const root = opts.mailbox || await findMailbox(page);
  if (!root) return { pageID: '', attachID: '', fileName: '' };

  const queue = [root];
  const seen = new Set([root]);
  let scanned = 0;
  while (queue.length && scanned < 200) {
    const id = queue.shift();
    scanned++;
    const body = await bodyOf(page, id);
    // 通信記録かどうかは `チャネル` タグで判る（受信も送信も持つ）。
    if (body.includes('<dt>' + CHANNEL_TAG + '</dt>')) {
      const m = body.match(/href="\/(\d{6})\/([a-z0-9]+)\.pdf"/);
      if (m) return { pageID: m[1], attachID: m[2], fileName: m[2] + '.pdf' };
    }
    for (const c of await childrenOf(page, id)) {
      if (!seen.has(c.ID)) { seen.add(c.ID); queue.push(c.ID); }
    }
  }
  return { pageID: '', attachID: '', fileName: '' };
}

// findThreadPair は「返信の鎖でつながった2枚」を探し、`{prev, next}` を返します
// （`prev` の `メッセージID` を `next` の `返信元メッセージID` が指している）。
//
// **鎖はヘッダの `In-Reply-To`** なので、受信どうしの返りも当たります——取り込んだ
// メールの中に1組あれば足ります。見つからなければ両方とも空。
async function findThreadPair(page, opts = {}) {
  const root = opts.mailbox || await findMailbox(page);
  if (!root) return { prev: '', next: '' };
  const byMsgID = new Map();   // メッセージID → ページID
  const replyTo = [];          // [ページID, 返信元メッセージID]
  const queue = [root];
  const seen = new Set([root]);
  let scanned = 0;
  const pick = (body, tag) => {
    const m = body.match(new RegExp('<dt>' + tag + '</dt><dd>([^<]*)</dd>'));
    return m ? m[1].trim() : '';
  };
  while (queue.length && scanned < 200) {
    const id = queue.shift();
    scanned++;
    const body = await bodyOf(page, id);
    const mid = pick(body, 'メッセージID');
    const rid = pick(body, '返信元メッセージID');
    if (mid) byMsgID.set(mid, id);
    if (rid) replyTo.push([id, rid]);
    for (const c of await childrenOf(page, id)) {
      if (!seen.has(c.ID)) { seen.add(c.ID); queue.push(c.ID); }
    }
  }
  for (const [id, rid] of replyTo) {
    const prev = byMsgID.get(rid);
    if (prev && prev !== id) return { prev, next: id };
  }
  return { prev: '', next: '' };
}

// findPageWithTag は、そのタグ名を持つページを1つ探します（`メールアドレス` など）。
// 取引先の下だけを見ます——連絡先は取引先ページに載るためです。見つからなければ空。
async function findPageWithTag(page, tagName, opts = {}) {
  const boxTitle = opts.boxTitle || '取引先';
  const top = await childrenOf(page, '000000');
  const box = top.find(c => (c.Title || '').trim() === boxTitle);
  if (!box) return '';
  const queue = (await childrenOf(page, box.ID)).map(c => c.ID);
  let scanned = 0;
  while (queue.length && scanned < 100) {
    const id = queue.shift();
    scanned++;
    const body = await bodyOf(page, id);
    if (body.includes('<dt>' + tagName + '</dt>')) return id;
    for (const c of await childrenOf(page, id)) queue.push(c.ID);
  }
  return '';
}

module.exports = {
  MAILBOX_TITLE, CHANNEL_TAG,
  login, childrenOf, bodyOf, findMailbox, findRecordWithPDF, findThreadPair, findPageWithTag,
  makePage, deletePage,
};

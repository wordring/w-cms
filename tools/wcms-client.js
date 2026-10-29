// wcms-client.js — Node の道具（tools/*.js）が w-cms へ繋ぐ係です（2026-10-29）。
//
// tools/templates.js と tools/sample-data.js が、同じ「クッキーを持ち回る fetch」とログインを写していたのを寄せました。
//   - 証明書の確かめは tls-trust.js（同じ機械のときだけ確かめない）。client() を呼ぶと整えます。
//   - 口を呼ぶときは Origin を付けます（CSRF の守り）。Set-Cookie は名前ごとに入れ替えて持ち回ります。
//   - ログインはフォーム形式（JSON だと error=1 で 302）。成功したかの見方は道具ごとに違うので、呼ぶ側が応答を見ます。
//   - ログインの既定はローカル開発の管理者 a / a（どちらの道具もそうしていた）。
const { trustFor } = require('./tls-trust');

// client は当て先 base の係 { req, login, cookie } を返します。
function client(base) {
  trustFor(base);
  let cookie = '';
  async function req(method, p, { body, headers = {} } = {}) {
    const res = await fetch(base + p, { method, body, redirect: 'manual', headers: { Origin: base, Cookie: cookie, ...headers } });
    for (const c of (res.headers.getSetCookie ? res.headers.getSetCookie() : [])) {
      const kv = c.split(';')[0], name = kv.split('=')[0];
      cookie = cookie.split('; ').filter((x) => x && !x.startsWith(name + '=')).concat(kv).join('; ');
    }
    return res;
  }
  const login = (user = 'a', pass = 'a') => req('POST', '/api/login', {
    body: 'username=' + encodeURIComponent(user) + '&password=' + encodeURIComponent(pass),
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
  });
  return { req, login, cookie: () => cookie };
}

module.exports = { client };

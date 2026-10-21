// この端末で知らせを受けるかの切り替え（Web Push・2026-10-21 夜・internal/cms/webpush.go）。
//
// 画面の上の帯（ログアウトの隣）に「🔔」を置きます。押すと、この端末のブラウザに知らせの受け口を作って w-cms に覚えさせます
// （ブラウザが「通知を許可するか」を聞きます）。もう一度押すと、受け口を捨てます。受けているあいだは「🔔 受けています」。
//
// ⚠ 受け口を作れるのは、正しい証明書の HTTPS で開いたページだけです（自分で作った証明書を信頼していない端末・http では
//    ボタンを出さない）。iPhone は、ホーム画面に追加して開いたときだけ受けられます。
// ⚠ CSP strict——DOM は createElement＋textContent で作る。app.js に頼らない。
(function () {
    'use strict';
    if (!('serviceWorker' in navigator) || !('PushManager' in window) || !window.isSecureContext) return;
    const anchor = document.getElementById('w-logout-btn');
    if (!anchor) return;

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.id = 'w-push-btn';
    btn.className = 'logout-btn push-btn';
    anchor.insertAdjacentElement('beforebegin', btn);

    const b64ToBytes = (s) => {
        const pad = '='.repeat((4 - (s.length % 4)) % 4);
        const raw = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'));
        return Uint8Array.from(raw, (c) => c.charCodeAt(0));
    };
    const post = (url, body) => fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body || {}) });

    let reg = null;
    async function current() {
        reg = reg || await navigator.serviceWorker.register('/sw.js');
        return reg.pushManager.getSubscription();
    }
    function show(on) {
        btn.textContent = on ? '🔔 受けています' : '🔕 知らせを受ける';
        btn.title = on ? 'この端末で、届いたことの知らせを受けています（押すとやめます）'
            : 'この端末で、届いたことの知らせを受けます（ブラウザが許可を聞きます）';
        btn.dataset.on = on ? '1' : '';
    }
    async function turnOn() {
        const perm = await Notification.requestPermission();
        if (perm !== 'granted') {
            alert('知らせが許可されませんでした（ブラウザの設定で、このサイトの通知を許可してください）。');
            return false;
        }
        const key = await (await fetch('/api/push/key')).json();
        if (!key || !key.key) throw new Error('鍵を受け取れません');
        const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64ToBytes(key.key) });
        const r = await post('/api/push/subscribe', sub.toJSON());
        if (!r.ok) throw new Error('受け口を覚えられません（' + r.status + '）');
        await post('/api/push/test');
        return true;
    }
    async function turnOff() {
        const sub = await current();
        if (!sub) return;
        await post('/api/push/unsubscribe', { endpoint: sub.endpoint });
        await sub.unsubscribe();
    }
    btn.addEventListener('click', async () => {
        btn.disabled = true;
        try {
            if (btn.dataset.on) {
                await turnOff();
                show(false);
            } else {
                show(await turnOn());
            }
        } catch (e) {
            alert('知らせの設定ができませんでした: ' + e.message);
        } finally {
            btn.disabled = false;
        }
    });
    current().then((s) => show(!!s)).catch(() => btn.remove());
})();

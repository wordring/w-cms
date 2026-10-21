// 知らせを受けて出す、ブラウザの裏の働き（Service Worker・2026-10-21 夜・internal/cms/webpush.go）。
//
// サーバーは GET /sw.js で配ります（置き場が「/」の直下でないと、ほかのページに働かない）。
// 届いた知らせ（JSON: title・body・url・tag）を端末の知らせに出し、押されたらその url を開きます（開いているタブがあれば、そこへ）。
'use strict';

self.addEventListener('push', (event) => {
    let m = {};
    try {
        m = event.data ? event.data.json() : {};
    } catch (e) {
        m = { title: 'w-cms', body: event.data ? event.data.text() : '' };
    }
    const title = m.title || 'w-cms';
    event.waitUntil(self.registration.showNotification(title, {
        body: m.body || '',
        tag: m.tag || undefined,
        renotify: !!m.tag,
        data: { url: m.url || '/' },
    }));
});

self.addEventListener('notificationclick', (event) => {
    event.notification.close();
    const url = new URL((event.notification.data && event.notification.data.url) || '/', self.location.origin).href;
    event.waitUntil((async () => {
        const wins = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
        for (const w of wins) {
            if (w.url === url && 'focus' in w) return w.focus();
        }
        for (const w of wins) {
            if ('navigate' in w && 'focus' in w) {
                await w.navigate(url);
                return w.focus();
            }
        }
        return self.clients.openWindow(url);
    })());
});

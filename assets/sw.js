// 通知を受けて出す、ブラウザの裏の働き（Service Worker・2026-10-21 夜・internal/cms/webpush.go）。
//
// サーバーは GET /sw.js で配ります（置き場が「/」の直下でないと、ほかのページに働かない）。
// 届いた通知（JSON: title・body・url・tag）を端末の通知に出し、押されたらその url を開きます
// （同じ url のタブが開いていれば、そこへ。ほかのタブは書きかけがあるかもしれないので、乗っ取らずに新しいタブで開く）。
'use strict';

// 入れ替わったら、すぐ働く（いま開いているタブも、読み直さずに受け持つ）。
self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

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
            if (w.url === url && 'focus' in w) {
                try {
                    return await w.focus();
                } catch (e) {
                    // 前へ出せないときは、新しいタブで開く。
                }
            }
        }
        return self.clients.openWindow(url);
    })());
});

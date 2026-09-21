import assert from 'node:assert/strict';
import test from 'node:test';

import * as extensionConfig from '../extension.json';
import { ensureHeaderMenusVisible, HEADER_MENU_RETRY_DELAYS_MS } from './header-menu';

function stubEda(impl: { insert?: (menus: unknown) => unknown }): { logs: string[] } {
	const logs: string[] = [];
	(globalThis as any).eda = {
		sys_HeaderMenu: {
			insertHeaderMenus: async (menus: unknown) => impl.insert?.(menus),
		},
		sys_Log: { add: (m: string) => logs.push(m) },
	};
	return { logs };
}

/** 收集等待時間,測試裡不真的等。 */
function fakeDelay(): { waits: number[]; fn: (ms: number) => Promise<void> } {
	const waits: number[] = [];
	return { waits, fn: async (ms: number) => { waits.push(ms); } };
}

// 实测(3.2.149):isShowAtHeaderMenu=true 时,声明式菜单与 activate 当下插入的都到不了
// 顶部菜单栏,只有晚一点插的才留得住。所以必须重插几次,而不是插一次就算。
test('header menu: registration is retried while the editor settles', async () => {
	const inserted: unknown[] = [];
	stubEda({ insert: menus => { inserted.push(menus); } });
	const delay = fakeDelay();
	try {
		await ensureHeaderMenusVisible(extensionConfig, delay.fn);

		assert.equal(inserted.length, HEADER_MENU_RETRY_DELAYS_MS.length,
			'every scheduled attempt must actually register');
		// 第一次不等待 —— 没有这个缺陷的宿主不该因此变慢。
		assert.deepEqual(delay.waits, HEADER_MENU_RETRY_DELAYS_MS.filter(d => d > 0));
		assert.ok(delay.waits.length > 0, 'a single immediate attempt is exactly the case that was measured to fail');
		for (const got of inserted) {
			assert.deepEqual(got, (extensionConfig as any).headerMenus,
				'what is registered must be the manifest itself, so the two cannot drift');
		}
	}
	finally { delete (globalThis as any).eda; }
});

test('header menu: the manifest entry carries the daemon-free recovery path', async () => {
	const menus = (extensionConfig as any).headerMenus;
	// 编辑器页面的上下文要在里面,否则打开 PCB/原理图时仍然看不到菜单。
	for (const ctx of ['pcb', 'sch']) {
		assert.ok(Array.isArray(menus[ctx]) && menus[ctx].length > 0, `${ctx} menus missing`);
	}
	const top = menus.pcb[0];
	assert.equal(top.id, 'EDA Agent');
	assert.ok(top.menuItems.some((i: any) => i.registerFn === 'reconnect'),
		'Reconnect is the only recovery path that does not need the daemon; it must be in the menu');
});

test('header menu: a rejecting host is reported once and not hammered', async () => {
	let calls = 0;
	const { logs } = stubEda({ insert: () => { calls++; throw new Error('host says no'); } });
	const delay = fakeDelay();
	try {
		// 不抛就是通过:activate() 不 await 它,抛出去会变成未处理的 rejection。
		await ensureHeaderMenusVisible(extensionConfig, delay.fn);
		assert.equal(calls, 1, 'a host that refuses will keep refusing — retrying is just noise');
		assert.equal(logs.filter(l => l.includes('header menu registration failed')).length, 1);
	}
	finally { delete (globalThis as any).eda; }
});

test('header menu: nothing to register is not an error', async () => {
	let called = false;
	stubEda({ insert: () => { called = true; } });
	const delay = fakeDelay();
	try {
		await ensureHeaderMenusVisible({}, delay.fn);
		await ensureHeaderMenusVisible(undefined, delay.fn);
		await ensureHeaderMenusVisible(null, delay.fn);
		assert.equal(called, false, 'no headerMenus in the manifest → no host call');
		assert.deepEqual(delay.waits, [], 'and no waiting either');
	}
	finally { delete (globalThis as any).eda; }
});

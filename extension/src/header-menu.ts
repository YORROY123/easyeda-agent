/// <reference types="@jlceda/pro-api-types" />
/**
 * Make the manifest's header menus actually appear.
 *
 * ## What breaks
 *
 * With `isShowAtHeaderMenu` on — which is what the connector ships with — the
 * `EDA Agent` menu never appears on EasyEDA Pro 3.2.149, not even while the
 * connector is connected and serving requests. `Reconnect` therefore cannot be
 * clicked, and that is the only recovery path which does not need the daemon —
 * exactly what an operator wants when the connector failed to come up (#221).
 *
 * ## What was measured
 *
 * Two independent extensions on 3.2.149 (the connector, and a throwaway probe
 * with its own uuid), registering the same menu three ways:
 *
 * | registered                   | isShowAtHeaderMenu=false | =true |
 * |------------------------------|--------------------------|-------|
 * | declared in extension.json   | appears (under Advanced) | never |
 * | `insertHeaderMenus` in activate() | —                   | never |
 * | `insertHeaderMenus` ~6s later     | —                   | appears (menu bar) |
 *
 * So the host does read and render declared menus — the probe's JS failed to
 * load entirely and its menu still showed up. The failure is specific to the
 * menu-bar path: entries registered while the editor is still coming up are
 * dropped, and only a later registration survives. Both `insertHeaderMenus`
 * calls reported success; nothing throws, the entries simply never reach the
 * bar.
 *
 * ## What this does about it
 *
 * Register, then register again a few times while the editor settles. The host
 * de-duplicates menu entries by id, so repeating is a no-op once it sticks —
 * which is why this retries blindly instead of trying to detect the moment the
 * menu bar is ready. There is no API to ask.
 *
 * The menus come from `extension.json`, which index.ts already imports, so the
 * runtime registration cannot drift from the manifest.
 */

/**
 * Delays (ms) at which the menus are registered. The first is immediate so a
 * host that does not have the defect pays nothing; the later ones cover the
 * window in which the measured host drops them.
 */
export const HEADER_MENU_RETRY_DELAYS_MS = [0, 1_000, 3_000, 8_000];

/** Injection seam: real timers in production, collected calls in tests. */
export type DelayFn = (ms: number) => Promise<void>;

const realDelay: DelayFn = ms => new Promise(resolve => setTimeout(resolve, ms));

/**
 * Register the manifest's header menus, repeatedly, until the host keeps them.
 * Never throws and never rejects: a menu is a convenience, and neither
 * activation nor the transport may depend on it.
 *
 * @param config - the parsed `extension.json`; its `headerMenus` is the single
 * source of truth.
 * @param delay - override the wait between attempts (tests inject a fake).
 */
export async function ensureHeaderMenusVisible(config: unknown, delay: DelayFn = realDelay): Promise<void> {
	const menus = (config as { headerMenus?: ISYS_HeaderMenus } | null | undefined)?.headerMenus;
	if (!menus) {
		return;
	}
	for (const wait of HEADER_MENU_RETRY_DELAYS_MS) {
		if (wait > 0) {
			await delay(wait);
		}
		try {
			await eda.sys_HeaderMenu.insertHeaderMenus(menus);
		}
		catch (err) {
			try {
				eda.sys_Log.add(`[easyeda-agent] header menu registration failed: ${String(err)}`);
			}
			catch { /* log panel unavailable */ }
			return; // A rejecting host will not start accepting; stop retrying.
		}
	}
}

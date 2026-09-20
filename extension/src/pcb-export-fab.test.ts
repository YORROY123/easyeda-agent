/// <reference types="@jlceda/pro-api-types" />
/**
 * Unit tests for the PCB manufacturing exports (`pcb.export.gerber` /
 * `pcb.export.pick_and_place` / `pcb.export.model3d`).
 *
 * Four endings are covered for each: a File comes back (and rides the standard
 * inlineBase64 artifact path), the @beta API answers `undefined`, the active
 * document is not a PCB, and the caller passed an argument the API does not
 * accept. No EasyEDA runtime needed — `eda.pcb_ManufactureData` is stubbed.
 */

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { runAction } from './actions';

/**
 * Install a fake `eda` carrying a stubbed `pcb_ManufactureData`, plus the
 * `EDMT_EditorDocumentType` runtime global that `documentTypeLabel` resolves.
 *
 * @param t - the node:test context, used to restore the globals afterwards
 * @param options - active documentType (3 = PCB) and per-API overrides
 * @returns recorded call arguments, keyed by API nickname
 */
function installManufactureStub(t: { after: (fn: () => void) => void }, options: {
	documentType?: number;
	documentReadFails?: boolean;
	gerber?: (...args: unknown[]) => Promise<unknown>;
	pnp?: (...args: unknown[]) => Promise<unknown>;
	model3d?: (...args: unknown[]) => Promise<unknown>;
} = {}): Record<string, unknown[][]> {
	const globals = globalThis as any;
	const previousEda = globals.eda;
	const previousTypes = globals.EDMT_EditorDocumentType;
	t.after(() => {
		if (previousEda === undefined) delete globals.eda;
		else globals.eda = previousEda;
		if (previousTypes === undefined) delete globals.EDMT_EditorDocumentType;
		else globals.EDMT_EditorDocumentType = previousTypes;
	});
	globals.EDMT_EditorDocumentType = {
		HOME: -1, BLANK: 0, SCHEMATIC_PAGE: 1, SYMBOL_COMPONENT: 2, PCB: 3, FOOTPRINT: 4, PANEL: 5,
	};
	const calls: Record<string, unknown[][]> = { gerber: [], pnp: [], model3d: [] };
	globals.eda = {
		dmt_Project: { getCurrentProjectInfo: async () => ({ uuid: 'p1', name: 'demo' }) },
		dmt_SelectControl: {
			getCurrentDocumentInfo: async () => {
				if (options.documentReadFails) throw new Error('identity unavailable');
				return { uuid: 'doc-1', tabId: 'tab-1', documentType: options.documentType ?? 3 };
			},
		},
		pcb_ManufactureData: {
			getGerberFile: async (...args: unknown[]) => {
				calls.gerber.push(args);
				// 0x50 0x4b 0x03 0x04 = "PK\x03\x04", a real ZIP local-file header.
				return options.gerber
					? options.gerber(...args)
					: new File([new Uint8Array([0x50, 0x4b, 0x03, 0x04])], 'Gerber_demo.zip', { type: 'application/zip' });
			},
			getPickAndPlaceFile: async (...args: unknown[]) => {
				calls.pnp.push(args);
				return options.pnp ? options.pnp(...args) : new File(['Designator\tX\tY\n'], 'PickAndPlace.csv');
			},
			get3DFile: async (...args: unknown[]) => {
				calls.model3d.push(args);
				return options.model3d ? options.model3d(...args) : new File([new Uint8Array([1, 2, 3])], 'Model3D.step');
			},
		},
	};
	return calls;
}

// ─── Gerber ──────────────────────────────────────────────────────────

test('pcb.export.gerber returns the ZIP through the standard artifact path', async (t) => {
	const calls = installManufactureStub(t);
	const res: any = await runAction('pcb.export.gerber', {
		fileName: 'MyBoard_Gerber',
		unit: 'MM',
		colorSilkscreen: true,
		digitalFormat: { integerNumber: 2, decimalNumber: 6 },
	});
	assert.deepEqual(calls.gerber, [['MyBoard_Gerber', true, 'mm', { integerNumber: 2, decimalNumber: 6 }]]);
	assert.equal(res.artifacts.length, 1);
	assert.equal(res.artifacts[0].kind, 'pcb_gerber');
	assert.equal(res.artifacts[0].fileName, 'Gerber_demo.zip');
	assert.equal(res.artifacts[0].mimeType, 'application/zip');
	// The binary must ride inlineBase64 — the same transport pcb.export.dsn,
	// pcb.snapshot and bom export already use. No new file channel.
	assert.equal(res.artifacts[0].inlineBase64, 'UEsDBA==');
	assert.equal(res.result.artifactId, res.artifacts[0].id);
	assert.equal(res.result.size, 4);
	assert.equal(res.result.unit, 'mm');
	assert.equal(res.result.colorSilkscreen, true);
});

test('pcb.export.gerber leaves every omitted optional argument undefined', async (t) => {
	const calls = installManufactureStub(t);
	await runAction('pcb.export.gerber', {});
	assert.deepEqual(calls.gerber, [['Gerber', undefined, undefined, undefined]]);
});

test('pcb.export.gerber reports an undefined platform result as a typed error', async (t) => {
	installManufactureStub(t, { gerber: async () => undefined });
	await assert.rejects(
		() => runAction('pcb.export.gerber', {}),
		(err: any) => {
			assert.equal(err.code, 'EDA_CALL_FAILED');
			assert.match(err.message, /getGerberFile \(@beta\) answered undefined/);
			return true;
		},
	);
});

test('pcb.export.gerber refuses a non-PCB active document without calling the API', async (t) => {
	const calls = installManufactureStub(t, { documentType: 1 });
	await assert.rejects(
		() => runAction('pcb.export.gerber', {}),
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED');
			assert.match(err.message, /active document is "schematic"/);
			return true;
		},
	);
	assert.deepEqual(calls.gerber, [], 'a refused precondition must not reach eda.*');
});

test('pcb.export.gerber proceeds when the document identity probe itself fails', async (t) => {
	// Fail-OPEN: the host can render a live PCB while every DMT metadata lookup
	// is empty (#190/#200), so an unreadable probe must not become a refusal.
	const calls = installManufactureStub(t, { documentReadFails: true });
	const res: any = await runAction('pcb.export.gerber', {});
	assert.equal(calls.gerber.length, 1);
	assert.equal(res.artifacts[0].kind, 'pcb_gerber');
});

test('pcb.export.gerber refuses mil — that unit belongs to the坐标文件 API', async (t) => {
	const calls = installManufactureStub(t);
	await assert.rejects(
		() => runAction('pcb.export.gerber', { unit: 'mil' }),
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED');
			assert.match(err.message, /unit must be one of mm \| inch/);
			return true;
		},
	);
	assert.deepEqual(calls.gerber, []);
});

test('pcb.export.gerber refuses a malformed digitalFormat', async (t) => {
	const calls = installManufactureStub(t);
	await assert.rejects(
		() => runAction('pcb.export.gerber', { digitalFormat: { integerNumber: 2, decimalNumber: 0 } }),
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED');
			assert.match(err.message, /digitalFormat must be/);
			return true;
		},
	);
	assert.deepEqual(calls.gerber, []);
});

// ─── Pick and place (坐标文件) ─────────────────────────────────────────

test('pcb.export.pick_and_place defaults to csv and carries the UTF-16 TSV caveat', async (t) => {
	const calls = installManufactureStub(t);
	const res: any = await runAction('pcb.export.pick_and_place', {});
	assert.deepEqual(calls.pnp, [['PickAndPlace', 'csv', undefined]]);
	assert.equal(res.artifacts[0].kind, 'pcb_pick_and_place');
	assert.equal(res.result.fileType, 'csv');
	assert.match(res.result.encodingCaveat, /UTF-16 TAB-separated/);
});

test('pcb.export.pick_and_place passes xlsx + mil through and drops the csv caveat', async (t) => {
	const calls = installManufactureStub(t, {
		pnp: async () => new File([new Uint8Array([1, 2])], 'PnP.xlsx'),
	});
	const res: any = await runAction('pcb.export.pick_and_place', { fileType: 'XLSX', unit: 'mil', fileName: 'PnP' });
	assert.deepEqual(calls.pnp, [['PnP', 'xlsx', 'mil']]);
	assert.equal(res.result.encodingCaveat, null);
	assert.equal(res.artifacts[0].mimeType, 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet');
});

test('pcb.export.pick_and_place refuses inch — that unit belongs to the Gerber API', async (t) => {
	const calls = installManufactureStub(t);
	await assert.rejects(
		() => runAction('pcb.export.pick_and_place', { unit: 'inch' }),
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED');
			assert.match(err.message, /unit must be one of mm \| mil/);
			return true;
		},
	);
	assert.deepEqual(calls.pnp, []);
});

test('pcb.export.pick_and_place refuses an unknown fileType', async (t) => {
	const calls = installManufactureStub(t);
	await assert.rejects(
		() => runAction('pcb.export.pick_and_place', { fileType: 'tsv' }),
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED');
			assert.match(err.message, /fileType must be one of csv \| xlsx/);
			return true;
		},
	);
	assert.deepEqual(calls.pnp, []);
});

test('pcb.export.pick_and_place reports an undefined platform result as a typed error', async (t) => {
	installManufactureStub(t, { pnp: async () => undefined });
	await assert.rejects(
		() => runAction('pcb.export.pick_and_place', {}),
		(err: any) => {
			assert.equal(err.code, 'EDA_CALL_FAILED');
			assert.match(err.message, /getPickAndPlaceFile \(@beta\) answered undefined/);
			return true;
		},
	);
});

test('pcb.export.pick_and_place refuses a non-PCB active document', async (t) => {
	const calls = installManufactureStub(t, { documentType: 4 });
	await assert.rejects(
		() => runAction('pcb.export.pick_and_place', {}),
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED');
			assert.match(err.message, /active document is "footprint"/);
			return true;
		},
	);
	assert.deepEqual(calls.pnp, []);
});

// ─── 3D model ────────────────────────────────────────────────────────

test('pcb.export.model3d forwards the documented get3DFile argument order', async (t) => {
	const calls = installManufactureStub(t);
	const res: any = await runAction('pcb.export.model3d', {
		fileName: 'Board3D',
		fileType: 'obj',
		element: ['Component Model', 'Via'],
		modelMode: 'Parts',
		autoGenerateModels: true,
	});
	assert.deepEqual(calls.model3d, [['Board3D', 'obj', ['Component Model', 'Via'], 'Parts', true]]);
	assert.equal(res.artifacts[0].kind, 'pcb_model3d');
	assert.equal(res.result.fileType, 'obj');
	assert.equal(res.result.modelMode, 'Parts');
});

test('pcb.export.model3d refuses an unknown element or modelMode before dispatch', async (t) => {
	const calls = installManufactureStub(t);
	await assert.rejects(
		() => runAction('pcb.export.model3d', { element: ['Copper Pour'] }),
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED');
			assert.match(err.message, /element must be an array drawn from/);
			return true;
		},
	);
	await assert.rejects(
		() => runAction('pcb.export.model3d', { modelMode: 'outfit' }),
		(err: any) => {
			assert.equal(err.code, 'PRECONDITION_REFUSED');
			assert.match(err.message, /modelMode must be Outfit/);
			return true;
		},
	);
	assert.deepEqual(calls.model3d, []);
});

test('pcb.export.model3d reports an undefined platform result as a typed error', async (t) => {
	installManufactureStub(t, { model3d: async () => undefined });
	await assert.rejects(
		() => runAction('pcb.export.model3d', {}),
		(err: any) => {
			assert.equal(err.code, 'EDA_CALL_FAILED');
			assert.match(err.message, /get3DFile \(@beta\) answered undefined/);
			return true;
		},
	);
});

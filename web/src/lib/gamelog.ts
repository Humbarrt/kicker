// Texts for the game event log (spec 02 §11) and helpers shared by the game pages.
import type { GameEvent, Seat } from '#lib/api/client.ts';
import { players } from '#lib/players.svelte.ts';
import { t, tPlayer, tSide, type Key } from '#lib/i18n.svelte.ts';

/** A player's name, the guest for an unknown coin. */
export function playerName(id: number | null | undefined): string {
	const p = players.get(id);
	return p ? tPlayer(p) : id == null ? t('reg.unknown_guest') : '…';
}

/** One line of the log in words. */
export function eventText(e: GameEvent): string {
	const side = tSide(e.side);
	switch (e.type) {
		case 'finished': {
			const how = e.reason ? ' · ' + t(('board.reason.' + e.reason) as Key) : '';
			return (e.side ? t('ev.finished_win', { side }) : t('ev.finished') + ' · ' + t('board.draw')) + how;
		}
		case 'paused':
			return t('ev.paused') + (e.reason ? ' · ' + pauseReason(e.reason) : '');
		case 'player_joined':
		case 'player_left':
		case 'player_assigned':
			return t(('ev.' + e.type) as Key, { player: playerName(e.player_id), side });
		case 'goal_voided':
		case 'reverted':
			return t(('ev.' + e.type) as Key, { seq: e.target_seq ?? '?' });
		case 'rules_changed':
			return t('ev.rules_changed', { rule: e.rule ?? '', value: e.rule_value ?? '–' });
		case 'goal':
			return t('ev.goal', { side });
		default:
			return t(('ev.' + e.type) as Key);
	}
}

/** The seat in a slot, if taken. */
export function seatAt(seats: Seat[], slot: number): Seat | undefined {
	return seats.find((s) => s.slot === slot);
}

/** Why a game was paused. "queued" is from before 2026-11-23 (Up next): another game was resumed. */
export function pauseReason(reason: string): string {
	const r = reason === 'queued' ? 'switched' : reason;
	return ['manual', 'timeout', 'new_game', 'switched'].includes(r) ? t(('paused.reason.' + r) as Key) : t('ev.paused');
}

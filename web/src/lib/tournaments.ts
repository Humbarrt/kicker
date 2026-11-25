// Labels and names for tournaments (spec 09): team names from the player cache, round and
// bracket labels, durations.
import type { TEntrant, TPairing, TStage, TournamentOption, TournamentStage, TEstimate } from '#lib/api/client.ts';
import { players } from '#lib/players.svelte.ts';
import { t, tPlayer, type Key } from '#lib/i18n.svelte.ts';

/** A team's (or Monster DYP pair's) name: its players' names. */
export function teamName(e: TEntrant | null | undefined): string {
	if (!e) return t('tourn.tbd');
	if (e.bye) return t('tourn.bye');
	const names = e.players.map((id) => {
		const p = players.get(id);
		return p ? tPlayer(p) : '…';
	});
	return names.join(' & ');
}

export function formatName(f: string): string {
	return t(('tourn.format.' + f) as Key);
}

/** A format option's name ("2 groups → knockout"). */
export function optionName(o: Pick<TournamentOption, 'name' | 'stages'>): string {
	switch (o.name) {
		case 'groups_knockout':
			return t('tourn.option.groups_knockout', { g: o.stages[0].groups ?? 2 });
		case 'swiss_final':
			return t('tourn.option.swiss_final');
	}
	return formatName(o.stages[0].format);
}

/** The stages of a tournament as text: "Groups → Knockout". */
export function stagesName(stages: TournamentStage[]): string {
	return stages.map((s) => formatName(s.format)).join(' → ');
}

export function duration(minutes: number): string {
	const m = Math.max(1, Math.round(minutes / 5) * 5);
	if (m < 60) return t('tourn.minutes', { m });
	return t('tourn.hours', { h: Math.floor(m / 60), m: m % 60 });
}

export function estimateText(e: TEstimate): string {
	const games = Math.round(e.games);
	return t(e.up_to ? 'tourn.estimate_up_to' : 'tourn.estimate', { games, time: duration(e.minutes) });
}

/** The heading of a bracket round: final, semi-finals, … or "Round n". */
export function roundLabel(stage: TStage, bracket: string, round: number): string {
	if (bracket === 'GF') return t(round === 1 ? 'tourn.bracket.GF' : 'tourn.bracket.reset');
	if (bracket === '3rd') return t('tourn.bracket.third');
	if (bracket === 'W' && stage.format === 'knockout') {
		const last = Math.max(...stage.pairings.filter((p) => p.bracket === 'W').map((p) => p.round));
		if (round === last) return t('tourn.bracket.final');
		if (round === last - 1) return t('tourn.bracket.semi');
		if (round === last - 2) return t('tourn.bracket.quarter');
	}
	return t('tourn.round', { n: round });
}

/** A pairing's label wherever it's listed alone: "Group A · Round 2", "Semi-finals", … */
export function pairingLabel(stages: TStage[], p: TPairing): string {
	const stage = stages[p.stage];
	if (!stage) return '';
	if (stage.format === 'groups') return `${t('tourn.group', { g: p.bracket })} · ${t('tourn.round', { n: p.round })}`;
	if (stage.format === 'double_knockout' && (p.bracket === 'W' || p.bracket === 'L'))
		return `${t(('tourn.bracket.' + p.bracket) as Key)} · ${t('tourn.round', { n: p.round })}`;
	return roundLabel(stage, p.bracket, p.round);
}

/** The pairings of a stage by key. */
export function pairingByKey(stages: TStage[], key: string): TPairing | undefined {
	for (const s of stages) for (const p of s.pairings) if (p.key === key) return p;
	return undefined;
}

/** "3:1" for a best-of pairing, "10:7" for a single game. */
export function pairingScore(p: TPairing): string {
	if (p.state !== 'done') return '';
	if (p.walkover) return t('tourn.walkover');
	if (p.best_of > 1) return `${p.wins_a}:${p.wins_b}`;
	const g = p.games[p.games.length - 1];
	return g ? `${g.goals_a}:${g.goals_b}` : `${p.goals_a}:${p.goals_b}`;
}

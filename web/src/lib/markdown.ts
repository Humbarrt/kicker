// A small markdown renderer for release notes: headings, lists, paragraphs, **bold**, *italic*,
// `code` and https links. Everything is HTML-escaped first, so the output is safe for {@html}.

const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

function inline(s: string): string {
	return esc(s)
		.replace(/`([^`]+)`/g, '<code class="rounded bg-stone-100 px-1 text-[0.9em]">$1</code>')
		.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
		.replace(/(^|[^*])\*([^*\s][^*]*)\*/g, '$1<em>$2</em>')
		.replace(/\[([^\]]+)\]\((https:\/\/[^\s)]+)\)/g, '<a class="underline underline-offset-2" href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
}

export function markdown(text: string): string {
	const out: string[] = [];
	let list: 'ul' | 'ol' | '' = '';
	let para: string[] = [];
	const flush = () => {
		if (para.length) out.push(`<p>${inline(para.join(' '))}</p>`);
		para = [];
	};
	const close = () => {
		if (list) out.push(`</${list}>`);
		list = '';
	};
	for (const raw of text.split('\n')) {
		const line = raw.trimEnd();
		const h = line.match(/^(#{1,6})\s+(.*)$/);
		const ul = line.match(/^\s*[-*+]\s+(.*)$/);
		const ol = line.match(/^\s*\d+[.)]\s+(.*)$/);
		if (h) {
			flush();
			close();
			out.push(`<h4 class="${h[1].length <= 2 ? 'font-semibold' : 'font-medium'}">${inline(h[2])}</h4>`);
		} else if (ul || ol) {
			flush();
			const kind = ul ? 'ul' : 'ol';
			if (list !== kind) {
				close();
				out.push(kind === 'ul' ? '<ul class="list-disc space-y-1 pl-5">' : '<ol class="list-decimal space-y-1 pl-5">');
				list = kind;
			}
			out.push(`<li>${inline((ul ?? ol)![1])}</li>`);
		} else if (!line.trim()) {
			flush();
			close();
		} else {
			close();
			para.push(line.trim());
		}
	}
	flush();
	close();
	return out.join('\n');
}

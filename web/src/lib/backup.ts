// Shared by the backup settings and the setup wizard.
import type { BackupLocation } from "#lib/api/client.ts";
import { t } from "#lib/i18n.svelte.ts";

/** The name shown for a backup location: its own, or one from its kind. */
export function locName(l: BackupLocation | undefined): string {
	if (!l) return "";
	if (l.name) return l.name;
	if (l.kind === "local") return t("backup.local");
	if (l.kind === "usb") return t("backup.usb_named", { name: l.path });
	return l.path;
}

// The depends input of the publish actions: buildhost project names, separated by commas or whitespace.

/** Splits a depends input into project names. An empty input is null, which sends no field and leaves the stored list untouched. */
export function dependsList(raw: string): string[] | null {
	const names = raw.split(/[\s,]+/).filter((n) => n.length > 0);
	return names.length > 0 ? names : null;
}

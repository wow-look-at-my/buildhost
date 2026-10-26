// The globals the typescript action injects into a script, as the entry
// functions of these modules take them.

export interface ActionGlobals {
	core: typeof core;
	github: typeof github;
	context: typeof context;
	octokit: typeof octokit;
	getOctokit: typeof getOctokit;
	exec: typeof exec;
	inputs: typeof inputs;
	steps: typeof steps;
}

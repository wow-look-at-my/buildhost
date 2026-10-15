// The publish step of buildhost-publish-release.

import { publishRelease } from '../resources';

await publishRelease({ core, github, context, octokit, exec, inputs, steps });

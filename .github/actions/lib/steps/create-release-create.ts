// The create step of buildhost-create-release.

import { createRelease } from '../resources';

await createRelease({ core, github, context, octokit, exec, inputs, steps });

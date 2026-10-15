// The get step of buildhost-get.

import { getResource } from '../resources';

await getResource({ core, github, context, octokit, exec, inputs, steps });

// The publish step of buildhost-publish.

import { publish } from '../publish';

await publish({ core, github, context, octokit, exec, inputs, steps });

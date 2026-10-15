// The create step of buildhost-create-token.

import { createToken } from '../resources';

await createToken({ core, github, context, octokit, exec, inputs, steps });

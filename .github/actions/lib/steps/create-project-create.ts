// The create step of buildhost-create-project.

import { createProject } from '../resources';

await createProject({ core, github, context, octokit, exec, inputs, steps });

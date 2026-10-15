// The default-tags step of buildhost-publish-docker.

import { defaultTags } from '../docker';

await defaultTags({ core, github, context, octokit, exec, inputs, steps });

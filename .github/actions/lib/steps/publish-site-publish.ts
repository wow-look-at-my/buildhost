// The publish step of buildhost-publish-site.

import { publishSite } from '../site';

await publishSite({ core, github, context, octokit, exec, inputs, steps });

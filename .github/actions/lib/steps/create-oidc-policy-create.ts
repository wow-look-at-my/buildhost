// The create step of buildhost-create-oidc-policy.

import { createOidcPolicy } from '../resources';

await createOidcPolicy({ core, github, context, octokit, exec, inputs, steps });

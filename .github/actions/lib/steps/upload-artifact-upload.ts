// The upload step of buildhost-upload-artifact.

import { uploadArtifact } from '../upload-artifact';

await uploadArtifact({ core, github, context, octokit, exec, inputs, steps });

import assert from 'node:assert/strict';
import { shouldApplyInferredCapabilities } from '../src/pages/model-capability-policy';

// New models always take the inferred defaults.
assert.equal(shouldApplyInferredCapabilities({ isNewModel: true, openedUpstream: '', upstream: 'gpt-4o' }), true);

// Opening an existing model and saving other fields must not rewrite saved tags.
assert.equal(shouldApplyInferredCapabilities({ isNewModel: false, openedUpstream: 'gpt-4o', upstream: 'gpt-4o' }), false);
assert.equal(shouldApplyInferredCapabilities({ isNewModel: false, openedUpstream: 'gpt-4o', upstream: ' gpt-4o ' }), false);

// Changing the upstream name of an existing model applies the new lookup.
assert.equal(shouldApplyInferredCapabilities({ isNewModel: false, openedUpstream: 'gpt-4o', upstream: 'gpt-4o-mini' }), true);
console.log('model-capability-policy tests passed');

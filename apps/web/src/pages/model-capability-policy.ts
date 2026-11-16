// Decides whether a models.dev lookup may overwrite the capability tags in the
// model editor. Saved tags of an existing model are kept unless its upstream
// model name is actually changed, so editing other fields never rewrites them.
export function shouldApplyInferredCapabilities(options: {
  isNewModel: boolean;
  openedUpstream: string;
  upstream: string;
}): boolean {
  if (options.isNewModel) return true;
  return options.upstream.trim() !== options.openedUpstream.trim();
}

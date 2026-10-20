import { FilePreviewDialog as SharedFilePreviewDialog, type FilePreviewLocation } from "@domainry/knowledge-ui";
import { describeError } from "./errors.ts";
import { filePreviewSource, type ExpectedFile, type FileTarget } from "./file-preview-state.ts";

type Props = {
  target: FileTarget;
  title: string;
  expected?: ExpectedFile;
  location?: FilePreviewLocation;
  onClose(): void;
};

export function FilePreviewDialog({ target, title, expected, location, onClose }: Props) {
  return <SharedFilePreviewDialog source={filePreviewSource(target, title, expected, location)} formatError={describeError} onClose={onClose} />;
}

export const guideSteps = [
  { id: "documents", label: "Documents" },
  { id: "scanner", label: "Scanner" },
  { id: "model", label: "Model" },
  { id: "ready", label: "Ready" },
] as const;
export type GuideStep = typeof guideSteps[number]["id"];
export type ModelChoice = "ollama" | "bonsai" | "fm" | "rules";
export const modelChoices: { id: ModelChoice; name: string; description: string }[] = [
  { id: "ollama", name: "Ollama", description: "Use your installed Ollama model for local document suggestions." },
  { id: "bonsai", name: "Bonsai", description: "Install PrismML’s compact Bonsai 8B model from this guide." },
  { id: "fm", name: "Apple Foundation Models", description: "Use Apple Intelligence through the fm command, with no model download." },
  { id: "rules", name: "Local rules for now", description: "Start without AI. Read document text and review filing suggestions yourself." },
];

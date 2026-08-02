"use client";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { useT } from "../../i18n";
import { SettingsRow } from "./settings-layout";

/**
 * Select-sentinel for "no policy" (unset). The backend stores `NULL` for
 * unset and the rest of the product works with `string | null`, so the
 * select maps this sentinel to `null` and back.
 */
const UNSET_SENTINEL = "__unset__";

/**
 * Shared language-policy picker for workspace / project / agent settings.
 *
 * The selected BCP-47 code becomes the language agents use for task output
 * (comments, handoffs, blocker reports, summaries). It is intentionally a
 * separate control from the UI-language picker (`user.language`) and from
 * agent programming-language badges (`agent.language_codes`).
 *
 * Fallback contract: unset (`null`) inherits the parent level (agent >
 * project > workspace); unset everywhere keeps the current behavior. The
 * default option and its hint make that fallback explicit in the UI.
 */
export function LanguagePolicyField({
  value,
  disabled,
  onChange,
  triggerClassName,
}: {
  value: string | null;
  disabled?: boolean;
  onChange: (next: string | null) => void;
  triggerClassName?: string;
}) {
  const { t } = useT("settings");
  const title = t(($) => $.agent_language_policy.title);
  const items = [
    {
      value: UNSET_SENTINEL,
      label: t(($) => $.agent_language_policy.default_label),
    },
    { value: "ru", label: t(($) => $.agent_language_policy.options.ru) },
    { value: "en", label: t(($) => $.agent_language_policy.options.en) },
    { value: "zh-Hans", label: t(($) => $.agent_language_policy.options["zh-Hans"]) },
    { value: "ja", label: t(($) => $.agent_language_policy.options.ja) },
    { value: "ko", label: t(($) => $.agent_language_policy.options.ko) },
  ];

  return (
    <Select
      items={items}
      value={value ?? UNSET_SENTINEL}
      disabled={disabled}
      onValueChange={(next) =>
        onChange(next === UNSET_SENTINEL ? null : next)
      }
    >
      <SelectTrigger
        size="sm"
        className={triggerClassName ?? "w-full"}
        aria-label={title}
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent align="end">
        {items.map((item) => (
          <SelectItem key={item.value} value={item.value}>
            {item.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

/**
 * Full settings-row variant used by workspace settings and the agent
 * general tab. The row hint states the fallback contract; the inline hint
 * under the control appears only while unset.
 */
export function LanguagePolicyRow({
  value,
  disabled,
  onChange,
}: {
  value: string | null;
  disabled?: boolean;
  onChange: (next: string | null) => void;
}) {
  const { t } = useT("settings");
  return (
    <SettingsRow
      label={t(($) => $.agent_language_policy.title)}
      description={t(($) => $.agent_language_policy.hint)}
      size="select-wide"
    >
      <div>
        <LanguagePolicyField value={value} disabled={disabled} onChange={onChange} />
        {!value ? (
          <p className="mt-1 text-xs text-muted-foreground">
            {t(($) => $.agent_language_policy.default_hint)}
          </p>
        ) : null}
      </div>
    </SettingsRow>
  );
}

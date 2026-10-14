defmodule Realtime.EventTypeTest do
  use ExUnit.Case, async: true

  alias Realtime.EventType

  test "canonical, legacy and Unicode types match the original normalization exactly" do
    types = [
      "CAMPAIGN_PROGRESS",
      "EMAIL_RECEIVED",
      "CUSTOM_EVENT_123",
      "A__B",
      "",
      nil,
      :email_received,
      123,
      ~c"email.received",
      "campaign.progress",
      "Inbox:Updated",
      "email -- \t\n received",
      "  audit.created  ",
      "straße",
      "évent:créé",
      "İnbox",
      "ﬃ-contact",
      "𐐨-event",
      "email\u00A0received",
      "email\u000Breceived",
      "email/received"
    ]

    for type <- types do
      assert EventType.normalize(type) == original(type)
    end

    for first <- 0..127, last <- 0..127 do
      type = <<first, ?A, ?_, last>>
      assert EventType.normalize(type) == original(type)
    end
  end

  defp original(value),
    do: value |> to_string() |> String.upcase() |> String.replace(~r/[.:\s-]+/, "_")
end

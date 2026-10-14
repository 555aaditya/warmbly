defmodule Realtime.EventType do
  @moduledoc false

  def normalize(value) do
    type = to_string(value)

    if canonical?(type),
      do: type,
      else: type |> String.upcase() |> String.replace(~r/[.:\s-]+/, "_")
  end

  defp canonical?(<<>>), do: true

  defp canonical?(<<c, rest::binary>>) when c in ?A..?Z or c in ?0..?9 or c == ?_,
    do: canonical?(rest)

  defp canonical?(_), do: false
end

defmodule RealtimeWeb.UserSocketCredentialTest do
  use ExUnit.Case, async: false

  alias RealtimeWeb.UserSocket

  @ticket "eyJhbGciOiJIUzI1NiJ9.e30.sig"

  test "a ws ticket is read from the query string" do
    assert UserSocket.credential(%{"token" => @ticket}, %{}) == {:ok, @ticket}
  end

  test "an API key or OAuth token in the query string is refused by default" do
    assert UserSocket.credential(%{"token" => "wmbly_abc"}, %{}) ==
             {:error, :credential_in_query}

    assert UserSocket.credential(%{"token" => "wmat_abc"}, %{}) ==
             {:error, :credential_in_query}
  end

  test "an API key in the query string is read when the operator allows it" do
    Application.put_env(:realtime, :allow_query_credentials, true)
    on_exit(fn -> Application.delete_env(:realtime, :allow_query_credentials) end)

    assert UserSocket.credential(%{"token" => "wmbly_abc"}, %{}) == {:ok, "wmbly_abc"}
    assert UserSocket.credential(%{"token" => "wmat_abc"}, %{}) == {:ok, "wmat_abc"}
  end

  test "the header wins over the query string" do
    info = %{x_headers: [{"x-warmbly-token", "wmbly_header"}]}
    assert UserSocket.credential(%{"token" => "wmbly_query"}, info) == {:ok, "wmbly_header"}
  end

  test "an API key is read from the x-warmbly-token header" do
    info = %{x_headers: [{"x-forwarded-for", "1.2.3.4"}, {"x-warmbly-token", "wmbly_abc"}]}
    assert UserSocket.credential(%{}, info) == {:ok, "wmbly_abc"}
  end

  test "no credential at all is missing" do
    assert UserSocket.credential(%{}, %{x_headers: []}) == {:error, :missing_token}
    assert UserSocket.credential(%{"token" => ""}, %{}) == {:error, :missing_token}
  end

  test "socket params are filtered from logs" do
    filtered = Phoenix.Logger.filter_values(%{"token" => @ticket, "vsn" => "1.0.0"})
    assert filtered["token"] == "[FILTERED]"
    assert filtered["vsn"] == "1.0.0"
  end
end

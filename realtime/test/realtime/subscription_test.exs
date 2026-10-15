defmodule Realtime.SubscriptionTest do
  use ExUnit.Case, async: false

  alias Realtime.Subscription
  alias Realtime.Test.AuthRepo

  @defaults %{
    limit_ws_message_pm: 120,
    limit_ws_join_pm: 30,
    limit_ws_event_pm: 60,
    max_connections: 10
  }

  setup do
    start_supervised!(AuthRepo)
    %{user_id: Ecto.UUID.generate()}
  end

  test "multiple subscriptions use the highest limit in either order" do
    rows = [[240, 15, 100, 20], [60, 45, 80, 5]]

    for ordered <- [rows, Enum.reverse(rows)] do
      user_id = Ecto.UUID.generate()

      AuthRepo.respond_with(fn _sql, params ->
        assert params == [Ecto.UUID.dump!(user_id)]
        {:ok, %{rows: ordered}}
      end)

      assert Subscription.get_limits(user_id) == %{
               limit_ws_message_pm: 240,
               limit_ws_join_pm: 45,
               limit_ws_event_pm: 100,
               max_connections: 20
             }
    end
  end

  test "a single subscription retains explicit limits below defaults", %{user_id: user_id} do
    AuthRepo.respond_with(fn _sql, _params -> {:ok, %{rows: [[60, 15, 20, 5]]}} end)

    assert Subscription.get_limits(user_id) == %{
             limit_ws_message_pm: 60,
             limit_ws_join_pm: 15,
             limit_ws_event_pm: 20,
             max_connections: 5
           }
  end

  test "null limits fall back independently for each subscription", %{user_id: user_id} do
    AuthRepo.respond_with(fn _sql, _params ->
      {:ok, %{rows: [[nil, 15, nil, 5], [60, nil, 80, nil]]}}
    end)

    assert Subscription.get_limits(user_id) == %{@defaults | limit_ws_event_pm: 80}
  end

  test "no subscriptions use defaults", %{user_id: user_id} do
    AuthRepo.respond_with(fn _sql, _params -> {:ok, %{rows: []}} end)
    assert Subscription.get_limits(user_id) == @defaults
  end

  test "database failures use defaults", %{user_id: user_id} do
    AuthRepo.respond_with(fn _sql, _params -> {:error, :database_unavailable} end)
    assert Subscription.get_limits(user_id) == @defaults
  end
end

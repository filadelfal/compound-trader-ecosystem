CREATE TABLE IF NOT EXISTS trading_order_events (
    id BIGSERIAL PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES trading_orders(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    event_type VARCHAR(30) NOT NULL,
    event_key VARCHAR(120) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT trading_order_events_unique UNIQUE (order_id, event_type, event_key)
);

CREATE INDEX IF NOT EXISTS idx_order_events_order_created
    ON trading_order_events (order_id, created_at, id);
CREATE INDEX IF NOT EXISTS idx_order_events_user_created
    ON trading_order_events (user_id, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION record_order_created_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO trading_order_events(order_id,user_id,event_type,event_key,payload,created_at)
    VALUES(NEW.id,NEW.user_id,'created',NEW.client_order_id,
           jsonb_build_object('status',NEW.status,'symbol',NEW.symbol,'side',NEW.side,
                              'type',NEW.order_type,'quantity',NEW.quantity::text,
                              'limitPrice',NEW.limit_price::text),NEW.created_at)
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS trg_record_order_created_event ON trading_orders;
CREATE TRIGGER trg_record_order_created_event AFTER INSERT ON trading_orders
FOR EACH ROW EXECUTE FUNCTION record_order_created_event();

CREATE OR REPLACE FUNCTION record_order_status_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO trading_order_events(order_id,user_id,event_type,event_key,payload)
    VALUES(NEW.id,NEW.user_id,'status',NEW.status,
           jsonb_strip_nulls(jsonb_build_object('from',OLD.status,'to',NEW.status,
                              'rejectionReason',NEW.rejection_reason)))
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS trg_record_order_status_event ON trading_orders;
CREATE TRIGGER trg_record_order_status_event AFTER UPDATE OF status ON trading_orders
FOR EACH ROW WHEN (OLD.status IS DISTINCT FROM NEW.status)
EXECUTE FUNCTION record_order_status_event();

CREATE OR REPLACE FUNCTION record_order_fill_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO trading_order_events(order_id,user_id,event_type,event_key,payload,created_at)
    SELECT NEW.order_id,orders.user_id,'fill',NEW.execution_id,
           jsonb_build_object('executionId',NEW.execution_id,'quantity',NEW.quantity::text,
                              'price',NEW.price::text,'executedAt',NEW.executed_at),NEW.created_at
    FROM trading_orders orders WHERE orders.id=NEW.order_id
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS trg_record_order_fill_event ON trading_order_fills;
CREATE TRIGGER trg_record_order_fill_event AFTER INSERT ON trading_order_fills
FOR EACH ROW EXECUTE FUNCTION record_order_fill_event();

CREATE OR REPLACE FUNCTION record_order_route_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status='processing' AND OLD.attempt_count IS DISTINCT FROM NEW.attempt_count THEN
        INSERT INTO trading_order_events(order_id,user_id,event_type,event_key,payload)
        SELECT NEW.order_id,orders.user_id,'routed',NEW.attempt_count::text,
               jsonb_build_object('venue',NEW.venue,'attempt',NEW.attempt_count)
        FROM trading_orders orders WHERE orders.id=NEW.order_id
        ON CONFLICT DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS trg_record_order_route_event ON trading_order_dispatches;
CREATE TRIGGER trg_record_order_route_event AFTER UPDATE OF status, attempt_count ON trading_order_dispatches
FOR EACH ROW EXECUTE FUNCTION record_order_route_event();

INSERT INTO trading_order_events(order_id,user_id,event_type,event_key,payload,created_at)
SELECT id,user_id,'created',client_order_id,
       jsonb_build_object('status',status,'symbol',symbol,'side',side,'type',order_type,
                          'quantity',quantity::text,'limitPrice',limit_price::text),created_at
FROM trading_orders ON CONFLICT DO NOTHING;

INSERT INTO trading_order_events(order_id,user_id,event_type,event_key,payload,created_at)
SELECT fills.order_id,orders.user_id,'fill',fills.execution_id,
       jsonb_build_object('executionId',fills.execution_id,'quantity',fills.quantity::text,
                          'price',fills.price::text,'executedAt',fills.executed_at),fills.created_at
FROM trading_order_fills fills JOIN trading_orders orders ON orders.id=fills.order_id
ON CONFLICT DO NOTHING;

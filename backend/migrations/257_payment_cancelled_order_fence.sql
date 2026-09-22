-- A local cancellation is the final product decision. During a blue-green
-- rollout, older HTTP/WS writers must not turn that cancelled row back into a
-- fulfilment-eligible state after a newer binary has recorded the decision.
CREATE OR REPLACE FUNCTION public.enforce_payment_order_cancelled_status_immutable()
RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = pg_catalog, public
AS $$
BEGIN
    IF OLD.status = 'CANCELLED' AND NEW.status IS DISTINCT FROM OLD.status THEN
        RAISE EXCEPTION USING
            ERRCODE = '23514',
            CONSTRAINT = 'payment_orders_cancelled_status_immutable',
            MESSAGE = 'cancelled payment order status is immutable';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS payment_orders_cancelled_status_immutable_trigger ON public.payment_orders;

CREATE TRIGGER payment_orders_cancelled_status_immutable_trigger
BEFORE UPDATE OF status ON public.payment_orders
FOR EACH ROW
WHEN (OLD.status = 'CANCELLED' AND NEW.status IS DISTINCT FROM OLD.status)
EXECUTE FUNCTION public.enforce_payment_order_cancelled_status_immutable();

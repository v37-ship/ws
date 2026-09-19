-- +goose Up
-- Bu Nah Chinese Food Halal menu: two categories (food/beverage) with
-- Medium/Large variant pricing (either may be NULL, never both), optional
-- variant lists (pudding flavors, snack kinds), and a pre-order flag for
-- items delivered to homes on the day (jamu, puding, snack).

CREATE TABLE food_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category TEXT NOT NULL CHECK (category IN ('food', 'beverage')),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    price_m BIGINT CHECK (price_m IS NULL OR price_m >= 0),
    price_l BIGINT CHECK (price_l IS NULL OR price_l >= 0),
    CHECK (price_m IS NOT NULL OR price_l IS NOT NULL),
    variants TEXT NOT NULL DEFAULT '',
    pre_order BOOLEAN NOT NULL DEFAULT false,
    active BOOLEAN NOT NULL DEFAULT true,
    image_path TEXT NOT NULL DEFAULT '',
    image_url TEXT NOT NULL DEFAULT '',
    sort INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX food_items_cat_idx ON food_items(category, sort);

-- +goose Down
DROP INDEX food_items_cat_idx;
DROP TABLE food_items;

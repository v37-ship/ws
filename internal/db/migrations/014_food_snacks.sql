-- +goose Up
-- Snacks become their own menu section (Puding, Snack move out of
-- beverages), and the pre-order concept is removed entirely: jamu, puding
-- and snack are regular ready items with no badges or footnotes.

ALTER TABLE food_items DROP CONSTRAINT food_items_category_check;
ALTER TABLE food_items ADD CHECK (category IN ('food', 'beverage', 'snack'));

UPDATE food_items
	SET category = 'snack',
	    sort = CASE name WHEN 'Puding' THEN 1 WHEN 'Snack' THEN 2 ELSE sort END
	WHERE name IN ('Puding', 'Snack');

ALTER TABLE food_items DROP COLUMN pre_order;

-- +goose Down
ALTER TABLE food_items ADD COLUMN pre_order BOOLEAN NOT NULL DEFAULT false;
UPDATE food_items SET category = 'beverage' WHERE category = 'snack';
ALTER TABLE food_items DROP CONSTRAINT food_items_category_check;
ALTER TABLE food_items ADD CHECK (category IN ('food', 'beverage'));

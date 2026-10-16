-- Limited-edition hats (Hat Studio): the designer may cap how many copies the WOTAN hat store
-- will ever sell. NULL = unlimited (the default). A hat is sold out once sales_count reaches
-- quantity_limit; after that it is no longer buyable from the store and is only available on the
-- secondary market. The designer's own free copy is not a sale and does not count.
ALTER TABLE hats ADD COLUMN quantity_limit INTEGER DEFAULT NULL;

begin;

delete
from transaction_source
where value in ('game', 'server', 'payment');

delete
from player
where phone_number = '1';

commit;
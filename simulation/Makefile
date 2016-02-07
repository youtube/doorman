%_pb2.py: %.proto
	protoc --python_out=. $*.proto

all:  config_pb2.py client_state_pb2.py server_state_pb2.py protocol_pb2.py lease_pb2.py

clean:
	rm -f aap* *.pyc *_pb2.py *.csv *.txt

